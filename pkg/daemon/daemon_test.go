/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemon

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/config/daemonconfig"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"
)

func TestMain(m *testing.M) {
	// Initialize global config so DumpFile does not panic.
	cfg := &config.SnapshotterConfig{}
	cfg.Root = os.TempDir()
	_ = config.ProcessConfigurations(cfg)
	os.Exit(m.Run())
}

// minimalFuseConfig returns the minimal valid FuseDaemonConfig JSON for tests.
func minimalFuseConfig() []byte {
	cfg := daemonconfig.FuseDaemonConfig{
		Device: &daemonconfig.DeviceConfig{},
		Mode:   "direct",
	}
	cfg.Device.Backend.BackendType = "registry"
	b, _ := json.Marshal(cfg)
	return b
}

func TestUpdateAuthConfig(t *testing.T) {
	tests := []struct {
		name         string
		shared       bool
		snapshotID   string
		kc           *auth.PassKeyChain
		wantAPICall  bool
		wantAPIID    string
		wantDiskAuth string // expected Auth field on disk after update
	}{
		{
			name:         "shared daemon with basic auth",
			shared:       true,
			snapshotID:   "snap-1",
			kc:           &auth.PassKeyChain{Username: "user", Password: "pass"},
			wantAPICall:  true,
			wantAPIID:    "/snap-1",
			wantDiskAuth: "dXNlcjpwYXNz", // base64("user:pass")
		},
		{
			name:         "dedicated daemon with basic auth",
			shared:       false,
			snapshotID:   "",
			kc:           &auth.PassKeyChain{Username: "user", Password: "pass"},
			wantAPICall:  true,
			wantAPIID:    "/",
			wantDiskAuth: "dXNlcjpwYXNz",
		},
		{
			name:        "bearer token skips API call",
			shared:      false,
			snapshotID:  "",
			kc:          &auth.PassKeyChain{Username: "", Password: "mytoken"},
			wantAPICall: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()

			// Write minimal fuse config to the expected path.
			var configFile string
			if tt.shared && tt.snapshotID != "" {
				configFile = filepath.Join(dir, tt.snapshotID, "config.json")
			} else {
				configFile = filepath.Join(dir, "config.json")
			}
			require.NoError(t, os.MkdirAll(filepath.Dir(configFile), 0o755))
			require.NoError(t, os.WriteFile(configFile, minimalFuseConfig(), 0o644))

			// Set up mock API server.
			var apiCalled bool
			var gotID string
			var gotBody map[string]string

			sock := filepath.Join(dir, "api.sock")
			ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				apiCalled = true
				gotID = r.URL.Query().Get("id")
				_ = json.NewDecoder(r.Body).Decode(&gotBody)
				w.WriteHeader(http.StatusNoContent)
			}))
			listener, err := net.Listen("unix", sock)
			require.NoError(t, err)
			ts.Listener = listener
			ts.Start()
			defer ts.Close()

			// Build a daemon with the right state.
			var daemonMode config.DaemonMode
			if tt.shared {
				daemonMode = config.DaemonModeShared
			} else {
				daemonMode = config.DaemonModeDedicated
			}

			d := &Daemon{
				States: ConfigState{
					ConfigDir:  dir,
					FsDriver:   config.FsDriverFusedev,
					DaemonMode: daemonMode,
					APISocket:  sock,
				},
			}
			// Pre-create client to avoid WaitUntilSocketExisted.
			client, err := NewNydusClient(sock)
			require.NoError(t, err)
			d.client = client

			err = d.UpdateAuthConfig(tt.snapshotID, tt.kc)
			require.NoError(t, err)

			assert.Equal(t, tt.wantAPICall, apiCalled)
			if tt.wantAPICall {
				assert.Equal(t, tt.wantAPIID, gotID)
				assert.Equal(t, tt.kc.ToBase64(), gotBody["registry_auth"])
			}

			// Verify config file was updated on disk.
			if tt.wantDiskAuth != "" {
				cfg, err := daemonconfig.LoadFuseConfig(configFile)
				require.NoError(t, err)
				assert.Equal(t, tt.wantDiskAuth, cfg.Device.Backend.Config.Auth)
			}
		})
	}
}

// TestRemoveRafsInstanceRefcount checks that a duplicate Umount for an
// already-removed snapshot does not decrement the daemon refcount a second
// time. AddRafsInstance only IncRef()s when it adds, so RemoveRafsInstance
// must only DecRef() when the instance was really cached.
func TestRemoveRafsInstanceRefcount(t *testing.T) {
	d := &Daemon{
		States:    ConfigState{ID: "daemon-1"},
		RafsCache: rafs.NewRafsCache(),
	}

	r := &rafs.Rafs{SnapshotID: "snap-1", ImageID: "example.com/foo:latest"}
	d.AddRafsInstance(r)
	assert.Equal(t, int32(1), d.GetRef())
	assert.Equal(t, 1, d.RafsCache.Len())

	// First Umount: instance is cached, so it drops the reference.
	d.RemoveRafsInstance(r.SnapshotID)
	assert.Equal(t, int32(0), d.GetRef())
	assert.Equal(t, 0, d.RafsCache.Len())

	// Duplicate Umount for the same snapshot must be a no-op on the refcount.
	d.RemoveRafsInstance(r.SnapshotID)
	assert.Equal(t, int32(0), d.GetRef())
	assert.Equal(t, 0, d.RafsCache.Len())
}

func TestCleanupOrphanedRafsConfigs(t *testing.T) {
	configDir := t.TempDir()
	d := &Daemon{
		States: ConfigState{
			ConfigDir:  configDir,
			DaemonMode: config.DaemonModeShared,
		},
		RafsCache: rafs.NewRafsCache(),
	}
	d.RafsCache.Add(&rafs.Rafs{SnapshotID: "active"})

	for _, id := range []string{"active", "orphan"} {
		dir := filepath.Join(configDir, id)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644))
	}
	otherDir := filepath.Join(configDir, "not-a-rafs-config")
	require.NoError(t, os.MkdirAll(otherDir, 0o755))

	require.NoError(t, d.CleanupOrphanedRafsConfigs())
	assert.DirExists(t, filepath.Join(configDir, "active"))
	assert.NoDirExists(t, filepath.Join(configDir, "orphan"))
	assert.DirExists(t, otherDir)
}
