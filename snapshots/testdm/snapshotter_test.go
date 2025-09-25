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
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/snapshots"
)

func TestSnapshotter(t *testing.T) {
	// Create temporary directory for testing
	tempDir := t.TempDir()

	// Create fake device directory
	fakeDeviceDir := filepath.Join(tempDir, "dev", "mapper")
	err := os.MkdirAll(fakeDeviceDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create fake device directory: %v", err)
	}

	// Create a fake device file for testing
	fakeDevicePath := filepath.Join(fakeDeviceDir, "test-device")
	file, err := os.Create(fakeDevicePath)
	if err != nil {
		t.Fatalf("Failed to create fake device: %v", err)
	}
	file.Close()

	// Create configuration
	config := &Config{
		RootPath:          filepath.Join(tempDir, "metadata"),
		DeviceDir:         fakeDeviceDir,
		DefaultFileSystem: "ext4",
	}

	// Create snapshotter
	ctx := context.Background()
	snapshotter, err := NewSnapshotter(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create snapshotter: %v", err)
	}
	defer snapshotter.Close()

	// Test Mounts method
	t.Run("Mounts", func(t *testing.T) {
		mounts, err := snapshotter.Mounts(ctx, "test-device")
		if err != nil {
			t.Fatalf("Failed to get mounts: %v", err)
		}

		if len(mounts) != 1 {
			t.Fatalf("Expected 1 mount, got %d", len(mounts))
		}

		mount := mounts[0]
		if mount.Source != fakeDevicePath {
			t.Errorf("Expected source %s, got %s", fakeDevicePath, mount.Source)
		}
		if mount.Type != "ext4" {
			t.Errorf("Expected type ext4, got %s", mount.Type)
		}
	})

	// Test non-existent device
	t.Run("NonExistentDevice", func(t *testing.T) {
		_, err := snapshotter.Mounts(ctx, "non-existent")
		if err == nil {
			t.Fatal("Expected error for non-existent device")
		}
	})

	// Test Prepare method
	t.Run("Prepare", func(t *testing.T) {
		mounts, err := snapshotter.Prepare(ctx, "test-device", "",
			snapshots.WithLabels(map[string]string{"test": "label"}))
		if err != nil {
			t.Fatalf("Failed to prepare: %v", err)
		}

		if len(mounts) != 1 {
			t.Fatalf("Expected 1 mount, got %d", len(mounts))
		}

		if mounts[0].Source != fakeDevicePath {
			t.Errorf("Expected source %s, got %s", fakeDevicePath, mounts[0].Source)
		}
	})

	// Test Stat method
	t.Run("Stat", func(t *testing.T) {
		info, err := snapshotter.Stat(ctx, "test-device")
		if err != nil {
			t.Fatalf("Failed to stat: %v", err)
		}

		if info.Name != "test-device" {
			t.Errorf("Expected name test-device, got %s", info.Name)
		}
		if info.Kind != snapshots.KindActive {
			t.Errorf("Expected kind %v, got %v", snapshots.KindActive, info.Kind)
		}
	})

	// Test Usage method
	t.Run("Usage", func(t *testing.T) {
		usage, err := snapshotter.Usage(ctx, "test-device")
		if err != nil {
			t.Fatalf("Failed to get usage: %v", err)
		}

		// Our implementation should return zero usage
		if usage.Size != 0 || usage.Inodes != 0 {
			t.Errorf("Expected zero usage, got Size=%d, Inodes=%d", usage.Size, usage.Inodes)
		}
	})
}
