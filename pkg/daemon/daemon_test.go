/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemon

import (
	"encoding/json"
	"fmt"
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
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"
	"golang.org/x/sys/unix"
)

type teardownClient struct {
	NydusdClient
	calls     *[]string
	cullDone  bool
	unbindErr error
	cullErr   error
}

func (c *teardownClient) UnbindBlob(domainID, blobID string) error {
	*c.calls = append(*c.calls, fmt.Sprintf("unbind:%s:%s", domainID, blobID))
	return c.unbindErr
}

func (c *teardownClient) CullBlob(blobID string, background bool) (CullBlobResult, error) {
	*c.calls = append(*c.calls, fmt.Sprintf("cull:%s:%t", blobID, background))
	if c.cullDone {
		return CullBlobResult{Status: CullBlobDone}, c.cullErr
	}
	return CullBlobResult{Status: CullBlobPending, Reason: "open"}, c.cullErr
}

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

func TestSharedErofsUmountOrderAndRetry(t *testing.T) {
	tests := []struct {
		name      string
		mounted   bool
		umountErr error
		cullDone  bool
		unbindErr error
		cullErr   error
		wantErr   bool
		wantCalls []string
	}{
		{
			name:      "mounted teardown follows reverse creation order",
			mounted:   true,
			cullDone:  true,
			wantCalls: []string{"check", "umount", "unbind:domain:cookie", "cull:cookie:true"},
		},
		{
			name:      "busy mount does not mutate daemon state",
			mounted:   true,
			umountErr: unix.EBUSY,
			cullDone:  true,
			wantErr:   true,
			wantCalls: []string{"check", "umount"},
		},
		{
			name:      "retry skips an already unmounted mountpoint",
			cullDone:  true,
			wantCalls: []string{"check", "unbind:domain:cookie", "cull:cookie:true"},
		},
		{
			name:      "pending bootstrap cull retains teardown state",
			wantErr:   true,
			wantCalls: []string{"check", "unbind:domain:cookie", "cull:cookie:true"},
		},
		{
			name:      "domain unbind failure remains visible",
			unbindErr: fmt.Errorf("unbind failed"),
			wantErr:   true,
			wantCalls: []string{"check", "unbind:domain:cookie"},
		},
		{
			name:      "bootstrap cull failure remains visible",
			cullErr:   fmt.Errorf("cull failed"),
			wantErr:   true,
			wantCalls: []string{"check", "unbind:domain:cookie", "cull:cookie:true"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			client := &teardownClient{
				calls:     &calls,
				cullDone:  tt.cullDone,
				unbindErr: tt.unbindErr,
				cullErr:   tt.cullErr,
			}
			d := &Daemon{client: client}
			ra := &rafs.Rafs{
				SnapshotID: "snapshot",
				Mountpoint: "/mountpoint",
				Annotations: map[string]string{
					rafs.AnnoFsCacheDomainID: "domain",
					rafs.AnnoFsCacheID:       "cookie",
				},
			}
			isMountpoint := func(string) (bool, error) {
				calls = append(calls, "check")
				return tt.mounted, nil
			}
			umount := func(string) error {
				calls = append(calls, "umount")
				return tt.umountErr
			}

			err := d.sharedErofsUmountWith(ra, isMountpoint, umount)
			assert.Equal(t, tt.wantCalls, calls)
			if tt.wantErr {
				require.Error(t, err)
				if tt.name == "pending bootstrap cull retains teardown state" {
					require.ErrorIs(t, err, errdefs.ErrFscacheCullPending)
					var pending *errdefs.FscacheCullPendingError
					require.ErrorAs(t, err, &pending)
					require.Equal(t, "cookie", pending.BlobID)
					require.Equal(t, "open", pending.Reason)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}
