/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package store

import (
	"context"

	"github.com/containerd/nydus-snapshotter/pkg/daemon"
)

type DaemonStore struct {
	db *Database // save daemons in database
}

func NewDaemonStore(db *Database) (*DaemonStore, error) {
	return &DaemonStore{
		db: db,
	}, nil
}

// Generally, we should directly fetch daemon from upper cache layer.
// Let's plant a stub here.
func (s *DaemonStore) GetByDaemonID(id string) (*daemon.Daemon, error) {
	return nil, nil
}

// Generally, we should directly fetch daemon from upper cache layer.
// Let's plant a stub here.
func (s *DaemonStore) GetBySnapshotID(snapshotID string) (*daemon.Daemon, error) {
	return nil, nil
}

func (s *DaemonStore) Add(d *daemon.Daemon) error {
	// Save daemon info in case snapshotter restarts so that we can restore the
	// daemon states and reconnect the daemons.
	return s.db.SaveDaemon(context.TODO(), d)
}

func (s *DaemonStore) Update(d *daemon.Daemon) error {
	return s.db.UpdateDaemon(context.TODO(), d)
}

func (s *DaemonStore) Delete(d *daemon.Daemon) error {
	return s.db.DeleteDaemon(context.TODO(), d.ID)
}

func (s *DaemonStore) WalkDaemons(ctx context.Context, cb func(d *daemon.Daemon) error) error {
	return s.db.WalkDaemons(ctx, cb)
}

func (s *DaemonStore) CleanupDaemons(ctx context.Context) error {
	return s.db.CleanupDaemons(ctx)
}
