/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package filesystem

import (
	"testing"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/stretchr/testify/require"
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
