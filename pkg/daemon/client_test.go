/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
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

	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
)

var BTI = types.BuildTimeInfo{
	PackageVer: "1.1.0",
	GitCommit:  "67f4ecc7acee6dd37234e6a697e72ac09d6cc8ba",
	BuildTime:  "Thu, 28 Jan 2021 14:02:39 +0000",
	Profile:    "debug",
	Rustc:      "rustc 1.46.0 (04488afe3 2020-08-24)",
}

func prepareNydusServer(t *testing.T) (string, func()) {
	dir, _ := os.MkdirTemp("", "nydus-snapshotter-test")
	mockSocket := filepath.Join(dir, "nydusd.sock")

	_, err := os.Stat(mockSocket)
	if err == nil {
		_ = os.Remove(mockSocket)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		info := types.DaemonInfo{
			ID:      "testid",
			Version: BTI,
			State:   "RUNNING",
		}
		w.Header().Set("Content-Type", "application/json")
		j, _ := json.Marshal(info)
		_, err := w.Write(j)
		assert.Nil(t, err)
	}))
	unixListener, err := net.Listen("unix", mockSocket)
	require.Nil(t, err)
	ts.Listener = unixListener
	ts.Start()
	return mockSocket, func() {
		ts.Close()
	}
}

func TestNydusClient_CheckStatus(t *testing.T) {
	sock, dispose := prepareNydusServer(t)
	defer dispose()
	client, err := NewNydusClient(sock)
	require.Nil(t, err)
	info, err := client.GetDaemonInfo()
	require.Nil(t, err)
	assert.Equal(t, info.DaemonState(), types.DaemonStateRunning)
	assert.Equal(t, "testid", info.ID)
	assert.Equal(t, BTI, info.Version)
}

func TestUpdateConfig(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		params     map[string]string
		statusCode int
		respBody   string
		wantErr    bool
	}{
		{
			name:       "shared daemon with snapshot ID",
			id:         "/snap-1",
			params:     map[string]string{"registry_auth": "dXNlcjpwYXNz"},
			statusCode: http.StatusNoContent,
		},
		{
			name:       "dedicated daemon with root ID",
			id:         "/",
			params:     map[string]string{"registry_auth": "dXNlcjpwYXNz"},
			statusCode: http.StatusNoContent,
		},
		{
			name:       "server returns error",
			id:         "/snap-1",
			params:     map[string]string{"registry_auth": "dXNlcjpwYXNz"},
			statusCode: http.StatusInternalServerError,
			respBody:   `{"code":"EINVAL","message":"invalid config"}`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotID string
			var gotBody map[string]string

			dir := t.TempDir()
			sock := filepath.Join(dir, "api.sock")

			ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPut, r.Method)
				assert.Equal(t, "/api/v1/config", r.URL.Path)

				gotID = r.URL.Query().Get("id")
				_ = json.NewDecoder(r.Body).Decode(&gotBody)

				if tt.respBody != "" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tt.statusCode)
					_, _ = w.Write([]byte(tt.respBody))
				} else {
					w.WriteHeader(tt.statusCode)
				}
			}))

			listener, err := net.Listen("unix", sock)
			require.NoError(t, err)
			ts.Listener = listener
			ts.Start()
			defer ts.Close()

			client, err := NewNydusClient(sock)
			require.NoError(t, err)

			err = client.UpdateConfig(tt.id, tt.params)
			assert.Equal(t, tt.id, gotID)
			assert.Equal(t, tt.params, gotBody)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid config")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
