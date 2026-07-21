/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package filesystem

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	racache "github.com/containerd/nydus-snapshotter/pkg/rafs"
	"github.com/containerd/nydus-snapshotter/pkg/store"
)

func TestTryRetainSharedDaemonReplacesUpgradedDaemon(t *testing.T) {
	fs := &Filesystem{}

	oldDaemon := &daemon.Daemon{
		States: daemon.ConfigState{
			ID:       "shared-daemon",
			FsDriver: config.FsDriverFscache,
		},
	}
	fs.TryRetainSharedDaemon(oldDaemon)
	require.Same(t, oldDaemon, fs.fscacheSharedDaemon)
	require.Equal(t, int32(1), oldDaemon.GetRef())

	newDaemon := &daemon.Daemon{
		States: daemon.ConfigState{
			ID:       "shared-daemon",
			FsDriver: config.FsDriverFscache,
		},
	}
	newDaemon.IncRef()
	fs.TryRetainSharedDaemon(newDaemon)

	require.Same(t, newDaemon, fs.fscacheSharedDaemon)
	require.Equal(t, int32(1), oldDaemon.GetRef())
	require.Equal(t, int32(1), newDaemon.GetRef())
}

// RefreshUnderlyingFiles is called from the blob cache GC path for every
// instance with unknown usage, so a daemon whose API socket is gone must make
// it fail immediately rather than sit in the 10-second socket poll that
// d.GetClient performs.
func TestRefreshUnderlyingFilesFailsFastWithoutDaemonSocket(t *testing.T) {
	rootDir := t.TempDir()
	db, err := store.NewDatabase(rootDir)
	require.NoError(t, err)

	m, err := manager.NewManager(manager.Opt{
		Database:      db,
		RootDir:       rootDir,
		FsDriver:      config.FsDriverFusedev,
		RecoverPolicy: config.RecoverPolicyRestart,
	})
	require.NoError(t, err)

	d, err := daemon.NewDaemon(
		daemon.WithSocketDir(rootDir),
		daemon.WithConfigDir(rootDir),
		daemon.WithLogDir(rootDir),
		daemon.WithFsDriver(config.FsDriverFusedev),
		daemon.WithDaemonMode(config.DaemonModeDedicated),
	)
	require.NoError(t, err)
	require.NoError(t, m.AddDaemon(d))

	fs := &Filesystem{enabledManagers: map[string]*manager.Manager{config.FsDriverFusedev: m}}
	instance := &racache.Rafs{
		SnapshotID: "snap-1",
		DaemonID:   d.ID(),
		FsDriver:   config.FsDriverFusedev,
	}

	start := time.Now()
	err = fs.RefreshUnderlyingFiles(instance)
	require.True(t, errdefs.IsNotFound(err))
	require.Less(t, time.Since(start), 5*time.Second)
	require.Empty(t, instance.UnderlyingFiles)
}
