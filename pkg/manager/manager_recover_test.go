/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/config/daemonconfig"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/store"
)

// startMockNydusd serves a RUNNING DaemonInfo over a unix socket, delaying
// each response by delay to emulate a slow daemon.
func startMockNydusd(t *testing.T, sock string, delay time.Duration) {
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(delay)
		w.Header().Set("Content-Type", "application/json")
		info := types.DaemonInfo{ID: "testid", State: "RUNNING"}
		j, _ := json.Marshal(info)
		_, _ = w.Write(j)
	}))
	listener, err := net.Listen("unix", sock)
	require.NoError(t, err)
	ts.Listener = listener
	ts.Start()
	t.Cleanup(ts.Close)
}

// newRecoverTestDaemon persists a fusedev daemon record whose API socket and
// PID are the given ones, and returns the record's configuration directory.
func newRecoverTestDaemon(t *testing.T, m *Manager, id, sock string, pid int) string {
	configDir := filepath.Join(t.TempDir(), id)
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	cfg := daemonconfig.FuseDaemonConfig{
		Device: &daemonconfig.DeviceConfig{},
		Mode:   "direct",
	}
	cfg.Device.Backend.BackendType = "registry"
	b, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.json"), b, 0o644))

	d, err := daemon.NewDaemon()
	require.NoError(t, err)
	d.States = daemon.ConfigState{
		ID:         id,
		ProcessID:  pid,
		APISocket:  sock,
		DaemonMode: config.DaemonModeDedicated,
		FsDriver:   config.FsDriverFusedev,
		ConfigDir:  configDir,
	}
	require.NoError(t, m.AddDaemon(d))
	return configDir
}

func TestRecoverDaemonsConcurrently(t *testing.T) {
	db, err := store.NewDatabase(t.TempDir())
	require.NoError(t, err)
	m, err := NewManager(Opt{
		Database: db,
		FsDriver: config.FsDriverFusedev,
	})
	require.NoError(t, err)

	// A process that already exited and was reaped: its record must land in
	// recoveringDaemons without stalling recovery.
	gone := exec.Command("true")
	require.NoError(t, gone.Start())
	require.NoError(t, gone.Wait())

	const liveCount = 6
	const delay = 400 * time.Millisecond
	sockDir := t.TempDir()
	for i := range liveCount {
		sock := filepath.Join(sockDir, fmt.Sprintf("live-%d.sock", i))
		startMockNydusd(t, sock, delay)
		newRecoverTestDaemon(t, m, fmt.Sprintf("live-%d", i), sock, os.Getpid())
	}
	newRecoverTestDaemon(t, m, "dead-0", filepath.Join(sockDir, "dead-0.sock"), gone.Process.Pid)
	newRecoverTestDaemon(t, m, "dead-1", filepath.Join(sockDir, "dead-1.sock"), gone.Process.Pid)

	recovering := make(map[string]*daemon.Daemon)
	live := make(map[string]*daemon.Daemon)

	start := time.Now()
	require.NoError(t, m.recoverDaemons(context.Background(), &recovering, &live))
	elapsed := time.Since(start)

	assert.Len(t, live, liveCount)
	for i := range liveCount {
		assert.Contains(t, live, fmt.Sprintf("live-%d", i))
	}
	assert.Len(t, recovering, 2)
	assert.Contains(t, recovering, "dead-0")
	assert.Contains(t, recovering, "dead-1")

	// Serially the six live daemons alone cost >= 6*delay (2.4s). Concurrent
	// recovery is bounded by the slowest daemon; leave generous headroom for
	// slow CI machines.
	assert.Less(t, elapsed, 4*delay)
}

func TestRecoverDaemonsCommitsNothingOnProbeFailure(t *testing.T) {
	db, err := store.NewDatabase(t.TempDir())
	require.NoError(t, err)
	m, err := NewManager(Opt{
		Database: db,
		FsDriver: config.FsDriverFusedev,
	})
	require.NoError(t, err)

	sockDir := t.TempDir()
	sock := filepath.Join(sockDir, "live-0.sock")
	startMockNydusd(t, sock, 0)
	newRecoverTestDaemon(t, m, "live-0", sock, os.Getpid())

	// A record whose configuration cannot be reloaded fails its probe.
	configDir := newRecoverTestDaemon(t, m, "damaged-0", filepath.Join(sockDir, "damaged-0.sock"), os.Getpid())
	require.NoError(t, os.Remove(filepath.Join(configDir, "config.json")))

	recovering := make(map[string]*daemon.Daemon)
	live := make(map[string]*daemon.Daemon)

	require.Error(t, m.recoverDaemons(context.Background(), &recovering, &live))

	// Recovery failed before the commit phase: no partial results.
	assert.Empty(t, recovering)
	assert.Empty(t, live)
}
