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

package plugin

import (
	"errors"

	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/snapshots/testdm"
	"github.com/containerd/platforms"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type:   plugin.SnapshotPlugin,
		ID:     "testdm",
		Config: &testdm.Config{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ic.Meta.Platforms = append(ic.Meta.Platforms, platforms.DefaultSpec())

			config, ok := ic.Config.(*testdm.Config)
			if !ok {
				return nil, errors.New("invalid testdm configuration")
			}

			if config.RootPath == "" {
				config.RootPath = ic.Root
			}

			if config.DeviceDir == "" {
				config.DeviceDir = "/dev/mapper"
			}

			ic.Meta.Exports[plugin.SnapshotterRootDir] = config.RootPath
			return testdm.NewSnapshotter(ic.Context, config)
		},
	})
}
