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
