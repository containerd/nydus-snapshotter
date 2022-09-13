/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package process

import (
	"context"

	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/store"
)

// Nydus daemons and snapshots persistence storage.
type Store interface {
	GetByDaemonID(id string) (*daemon.Daemon, error)
	GetBySnapshotID(snapshotID string) (*daemon.Daemon, error)
	Add(*daemon.Daemon) error
	Update(d *daemon.Daemon) error
	Delete(*daemon.Daemon) error
	WalkDaemons(ctx context.Context, cb func(*daemon.Daemon) error) error
	CleanupDaemons(ctx context.Context) error
}

var _ Store = &store.DaemonStore{}
