/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	"context"

	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"
	"github.com/containerd/nydus-snapshotter/pkg/store"
)

// Nydus daemons and fs instances persistence storage.
type Store interface {
	// If the daemon is inserted to DB before, return error ErrAlreadyExisted.
	AddDaemon(d *daemon.Daemon) error
	UpdateDaemon(d *daemon.Daemon) error
	DeleteDaemon(id string) error
	WalkDaemons(ctx context.Context, cb func(*daemon.States) error) error
	CleanupDaemons(ctx context.Context) error

	AddInstance(r *rafs.Rafs) error
	DeleteInstance(snapshotID string) error
	WalkInstances(ctx context.Context, cb func(*rafs.Rafs) error) error

	NextInstanceSeq() (uint64, error)
}

var _ Store = &store.DaemonStore{}
