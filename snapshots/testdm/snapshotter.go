//go:build linux

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package testdm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/containerd/log"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/storage"
)

// Config represents configuration for the testdm plugin.
type Config struct {
	// RootPath is the directory for storing metadata
	RootPath string `toml:"root_path"`

	// DeviceDir is the directory where device mapper devices are located
	// Defaults to "/dev/mapper"
	DeviceDir string `toml:"device_dir"`

	// DefaultFileSystem is the filesystem type to use for mounts (ext4, xfs, etc.)
	DefaultFileSystem string `toml:"default_fs"`
}

// Snapshotter implements a minimal snapshotter that returns mounts for pre-existing devmapper devices
type Snapshotter struct {
	store     *storage.MetaStore
	config    *Config
	mu        sync.RWMutex
	closeOnce sync.Once
}

// NewSnapshotter creates a new testdm snapshotter
func NewSnapshotter(ctx context.Context, config *Config) (*Snapshotter, error) {
	if config.RootPath == "" {
		return nil, fmt.Errorf("root path cannot be empty")
	}

	if config.DeviceDir == "" {
		config.DeviceDir = "/dev/mapper"
	}

	if config.DefaultFileSystem == "" {
		config.DefaultFileSystem = "ext4"
	}

	// Create root directory
	if err := os.MkdirAll(config.RootPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create root directory %s: %w", config.RootPath, err)
	}

	// Initialize metadata store
	store, err := storage.NewMetaStore(filepath.Join(config.RootPath, "metadata.db"))
	if err != nil {
		return nil, fmt.Errorf("failed to create metadata store: %w", err)
	}

	log.G(ctx).WithFields(log.Fields{
		"root":       config.RootPath,
		"device_dir": config.DeviceDir,
		"default_fs": config.DefaultFileSystem,
	}).Info("testdm snapshotter initialized")

	return &Snapshotter{
		store:  store,
		config: config,
	}, nil
}

// Close releases resources
func (s *Snapshotter) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.store != nil {
			err = s.store.Close()
		}
	})
	return err
}

// getDevicePath returns the device path for a given snapshot key
func (s *Snapshotter) getDevicePath(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Construct device path by combining device directory with key
	devicePath := filepath.Join(s.config.DeviceDir, key)

	// Verify the device actually exists
	if _, err := os.Stat(devicePath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("device %s does not exist: %w", devicePath, errdefs.ErrNotFound)
		}
		return "", fmt.Errorf("failed to access device %s: %w", devicePath, err)
	}

	return devicePath, nil
}

// Stat returns info for a snapshot - creates minimal metadata
func (s *Snapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	log.G(ctx).WithField("key", key).Info("stat")

	var info snapshots.Info
	err := s.store.WithTransaction(ctx, false, func(ctx context.Context) error {
		var err error
		_, info, _, err = storage.GetInfo(ctx, key)
		return err
	})

	if err != nil {
		// If not found in metadata store, check if we have a device mapping for this key
		if errdefs.IsNotFound(err) {
			if _, err := s.getDevicePath(key); err != nil {
				return snapshots.Info{}, err
			}
			// Return minimal info for mapped device
			return snapshots.Info{
				Name:    key,
				Kind:    snapshots.KindActive,
				Created: time.Now(),
				Updated: time.Now(),
				Labels:  make(map[string]string),
			}, nil
		}
		return snapshots.Info{}, err
	}

	return info, nil
}

// Update updates snapshot info
func (s *Snapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	log.G(ctx).WithField("key", info.Name).Info("update")

	err := s.store.WithTransaction(ctx, true, func(ctx context.Context) error {
		var updateErr error
		info, updateErr = storage.UpdateInfo(ctx, info, fieldpaths...)
		return updateErr
	})

	return info, err
}

// Usage returns resource usage - returns zero usage for simplicity
func (s *Snapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	log.G(ctx).WithField("key", key).Info("usage")

	// Verify the key exists (either in metadata or device mapping)
	if _, err := s.Stat(ctx, key); err != nil {
		return snapshots.Usage{}, err
	}

	// Return zero usage since we don't track actual device usage
	return snapshots.Usage{
		Inodes: 0,
		Size:   0,
	}, nil
}

// Mounts returns mounts for the snapshot - this is the key method that returns device paths
func (s *Snapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	log.G(ctx).WithField("key", key).Info("mounts")

	devicePath, err := s.getDevicePath(key)
	if err != nil {
		return nil, err
	}

	// Return mount for the existing device
	mounts := []mount.Mount{
		{
			Source:  devicePath,
			Type:    s.config.DefaultFileSystem,
			Options: []string{"rw"},
		},
	}

	log.G(ctx).WithFields(log.Fields{
		"key":    key,
		"device": devicePath,
		"fs":     s.config.DefaultFileSystem,
	}).Info("returning mounts for existing device")

	return mounts, nil
}

// Prepare creates an active snapshot - in our case, just validate device exists
func (s *Snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	log.G(ctx).WithFields(log.Fields{
		"key":    key,
		"parent": parent,
	}).Info("prepare")

	// Check if device mapping exists for this key
	_, err := s.getDevicePath(key)
	if err != nil {
		return nil, err
	}

	// Create minimal metadata entry
	err = s.store.WithTransaction(ctx, true, func(ctx context.Context) error {
		_, err := storage.CreateSnapshot(ctx, snapshots.KindActive, key, parent, opts...)
		return err
	})
	if err != nil {
		return nil, err
	}

	// Return mounts for the existing device
	return s.Mounts(ctx, key)
}

// View creates a readonly snapshot - same as Prepare but readonly
func (s *Snapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	log.G(ctx).WithFields(log.Fields{
		"key":    key,
		"parent": parent,
	}).Info("view")

	// Check if device mapping exists for this key
	devicePath, err := s.getDevicePath(key)
	if err != nil {
		return nil, err
	}

	// Create minimal metadata entry
	err = s.store.WithTransaction(ctx, true, func(ctx context.Context) error {
		_, err := storage.CreateSnapshot(ctx, snapshots.KindView, key, parent, opts...)
		return err
	})
	if err != nil {
		return nil, err
	}

	// Return readonly mounts for the existing device
	mounts := []mount.Mount{
		{
			Source:  devicePath,
			Type:    s.config.DefaultFileSystem,
			Options: []string{"ro"},
		},
	}

	return mounts, nil
}

// Commit commits an active snapshot - just update metadata
func (s *Snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	log.G(ctx).WithFields(log.Fields{
		"name": name,
		"key":  key,
	}).Info("commit")

	var o snapshots.Info
	for _, opt := range opts {
		if err := opt(&o); err != nil {
			return err
		}
	}

	return s.store.WithTransaction(ctx, true, func(ctx context.Context) error {
		// Use CommitActive which is the correct storage API method
		_, err := storage.CommitActive(ctx, key, name, snapshots.Usage{}, opts...)
		return err
	})
}

// Remove removes a snapshot - just remove from metadata
func (s *Snapshotter) Remove(ctx context.Context, key string) error {
	log.G(ctx).WithField("key", key).Info("remove")

	return s.store.WithTransaction(ctx, true, func(ctx context.Context) error {
		_, _, err := storage.Remove(ctx, key)
		return err
	})
}

// Walk iterates through all snapshots
func (s *Snapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, fs ...string) error {
	log.G(ctx).Info("walk")

	return s.store.WithTransaction(ctx, false, func(ctx context.Context) error {
		return storage.WalkInfo(ctx, fn, fs...)
	})
}
