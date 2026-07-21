/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/store"
)

func TestRecoverDaemonsSkipsRecordsWithoutConfig(t *testing.T) {
	rootDir := t.TempDir()
	db, err := store.NewDatabase(rootDir)
	require.NoError(t, err)

	m, err := NewManager(Opt{
		Database:      db,
		RootDir:       rootDir,
		FsDriver:      config.FsDriverFusedev,
		RecoverPolicy: config.RecoverPolicyRestart,
	})
	require.NoError(t, err)

	// Two fusedev daemon records persisted without their config file on disk,
	// as left behind by a crash (or full disk) between persisting the record
	// and dumping the configuration.
	poisoned := make([]*daemon.Daemon, 0, 2)
	for range 2 {
		d, err := daemon.NewDaemon(
			daemon.WithSocketDir(rootDir),
			daemon.WithConfigDir(rootDir),
			daemon.WithLogDir(rootDir),
			daemon.WithFsDriver(config.FsDriverFusedev),
			daemon.WithDaemonMode(config.DaemonModeDedicated),
		)
		require.NoError(t, err)
		require.NoError(t, m.AddDaemon(d))
		poisoned = append(poisoned, d)
	}

	// A record of another fs driver must be ignored by this manager and
	// survive untouched.
	other, err := daemon.NewDaemon(
		daemon.WithSocketDir(rootDir),
		daemon.WithConfigDir(rootDir),
		daemon.WithLogDir(rootDir),
		daemon.WithFsDriver(config.FsDriverFscache),
		daemon.WithDaemonMode(config.DaemonModeShared),
	)
	require.NoError(t, err)
	require.NoError(t, m.AddDaemon(other))

	// Recovery must not abort on the damaged records: returning an error here
	// fails NewSnapshotter on every restart, permanently crash-looping the
	// snapshotter until the database is repaired by hand.
	recovering := make(map[string]*daemon.Daemon)
	live := make(map[string]*daemon.Daemon)
	require.NoError(t, m.recoverDaemons(context.Background(), &recovering, &live))
	assert.Empty(t, recovering)
	assert.Empty(t, live)

	// The damaged records stay in the store: a missing config file does not
	// prove the daemon is dead, and keeping the record lets a later restart
	// re-adopt the daemon once the file is restored.
	remaining := make(map[string]string)
	require.NoError(t, m.store.WalkDaemons(context.Background(), func(s *daemon.ConfigState) error {
		remaining[s.ID] = s.FsDriver
		return nil
	}))
	expected := map[string]string{other.ID(): config.FsDriverFscache}
	for _, d := range poisoned {
		expected[d.ID()] = config.FsDriverFusedev
	}
	assert.Equal(t, expected, remaining)

	// Only the in-memory registration is dropped, so no half-initialized
	// daemon without its configuration lingers in the cache.
	for _, d := range poisoned {
		assert.Nil(t, m.daemonCache.GetByDaemonID(d.ID(), nil))
	}
}
