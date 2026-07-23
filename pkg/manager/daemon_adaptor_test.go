/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
)

func TestStartDaemonUpgradeMode(t *testing.T) {
	tests := []struct {
		name        string
		upgrade     bool
		wantUpgrade bool
	}{
		{
			name:        "normal start",
			upgrade:     false,
			wantUpgrade: false,
		},
		{
			name:        "failover start",
			upgrade:     true,
			wantUpgrade: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := &config.SnapshotterConfig{
				Root:       root,
				DaemonMode: string(config.DaemonModeShared),
				DaemonConfig: config.DaemonConfig{
					FsDriver: config.FsDriverFscache,
				},
			}
			require.NoError(t, config.ProcessConfigurations(cfg))

			argsFile := filepath.Join(root, "args.txt")
			scriptPath := filepath.Join(root, "fake-nydusd.sh")
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + argsFile + "\"\n"
			require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0755))

			manager := &Manager{
				cacheDir:         root,
				daemonCache:      newDaemonCache(),
				NydusdBinaryPath: scriptPath,
			}
			d := &daemon.Daemon{
				States: daemon.ConfigState{
					ID:          "test-daemon",
					APISocket:   filepath.Join(root, "api.sock"),
					FsDriver:    config.FsDriverFscache,
					LogLevel:    "info",
					LogToStdout: true,
				},
			}

			require.NoError(t, manager.startDaemon(d, tc.upgrade))

			var args string
			require.Eventually(t, func() bool {
				content, err := os.ReadFile(argsFile)
				if err != nil {
					return false
				}
				args = strings.TrimSpace(string(content))
				return true
			}, time.Second, 10*time.Millisecond)
			if tc.wantUpgrade {
				require.Contains(t, args, "--upgrade")
			} else {
				require.NotContains(t, args, "--upgrade")
			}
		})
	}
}
