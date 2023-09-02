/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package store

import (
	"context"

	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"
)

type DaemonStore struct {
	db *Database // save daemons in database
}

func NewDaemonStore(db *Database) (*DaemonStore, error) {
	return &DaemonStore{
		db: db,
	}, nil
}

// If the daemon is inserted to DB before, return error ErrAlreadyExisted.
func (s *DaemonStore) AddDaemon(d *daemon.Daemon) error {
	// Save daemon info in case snapshotter restarts so that we can restore the
	// daemon states and reconnect the daemons.
	return s.db.SaveDaemon(context.TODO(), d)
}

func (s *DaemonStore) UpdateDaemon(d *daemon.Daemon) error {
	return s.db.UpdateDaemon(context.TODO(), d)
}

func (s *DaemonStore) DeleteDaemon(id string) error {
	return s.db.DeleteDaemon(context.TODO(), id)
}

func (s *DaemonStore) WalkDaemons(ctx context.Context, cb func(d *daemon.States) error) error {
	return s.db.WalkDaemons(ctx, cb)
}

func (s *DaemonStore) CleanupDaemons(ctx context.Context) error {
	return s.db.CleanupDaemons(ctx)
}

func (s *DaemonStore) AddInstance(r *rafs.Rafs) error {
	return s.db.AddInstance(context.TODO(), r)
}

func (s *DaemonStore) DeleteInstance(snapshotID string) error {
	return s.db.DeleteInstance(context.TODO(), snapshotID)
}

func (s *DaemonStore) NextInstanceSeq() (uint64, error) {
	return s.db.NextInstanceSeq()
}

func (s *DaemonStore) WalkInstances(ctx context.Context, cb func(*rafs.Rafs) error) error {
	return s.db.WalkInstances(ctx, cb)
}
