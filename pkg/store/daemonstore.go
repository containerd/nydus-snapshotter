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

type DaemonRafsStore struct {
	db *Database // save daemons in database
}

func NewDaemonRafsStore(db *Database) (*DaemonRafsStore, error) {
	return &DaemonRafsStore{
		db: db,
	}, nil
}

// If the daemon is inserted to DB before, return error ErrAlreadyExisted.
func (s *DaemonRafsStore) AddDaemon(d *daemon.Daemon) error {
	// Save daemon info in case snapshotter restarts so that we can restore the
	// daemon states and reconnect the daemons.
	return s.db.SaveDaemon(context.TODO(), d)
}

func (s *DaemonRafsStore) UpdateDaemon(d *daemon.Daemon) error {
	return s.db.UpdateDaemon(context.TODO(), d)
}

func (s *DaemonRafsStore) DeleteDaemon(id string) error {
	return s.db.DeleteDaemon(context.TODO(), id)
}

func (s *DaemonRafsStore) WalkDaemons(ctx context.Context, cb func(d *daemon.ConfigState) error) error {
	return s.db.WalkDaemons(ctx, cb)
}

func (s *DaemonRafsStore) CleanupDaemons(ctx context.Context) error {
	return s.db.CleanupDaemons(ctx)
}

func (s *DaemonRafsStore) AddRafsInstance(r *rafs.Rafs) error {
	return s.db.AddRafsInstance(context.TODO(), r)
}

func (s *DaemonRafsStore) DeleteRafsInstance(snapshotID string) error {
	return s.db.DeleteRafsInstance(context.TODO(), snapshotID)
}

func (s *DaemonRafsStore) WalkRafsInstances(ctx context.Context, cb func(*rafs.Rafs) error) error {
	return s.db.WalkRafsInstances(ctx, cb)
}

func (s *DaemonRafsStore) NextInstanceSeq() (uint64, error) {
	return s.db.NextInstanceSeq()
}
