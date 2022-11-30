/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package store

import (
	"context"
	"encoding/json"
	"path"

	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/pkg/errors"
	bolt "go.etcd.io/bbolt"
)

const SharedNydusDaemonID = "shared_daemon"

type CompatDaemon struct {
	ID               string
	SnapshotID       string
	ConfigDir        string
	SocketDir        string
	LogDir           string
	LogLevel         string
	LogToStdout      bool
	SnapshotDir      string
	Pid              int
	ImageID          string
	FsDriver         string
	APISock          *string
	RootMountPoint   *string
	CustomMountPoint *string
}

func (db *Database) WalkCompatDaemons(ctx context.Context, handler func(cd *CompatDaemon) error) error {

	return db.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(daemonsBucket)
		if bucket == nil {
			return errdefs.ErrNotFound
		}

		return bucket.ForEach(func(key, value []byte) error {
			d := &CompatDaemon{}

			if err := json.Unmarshal(value, d); err != nil {
				return errors.Wrapf(err, "unmarshal %s", key)
			}

			return handler(d)
		})
	})
}

func (db *Database) tryTranslateRecords() error {
	daemons := make([]*CompatDaemon, 0)

	err := db.WalkCompatDaemons(context.TODO(), func(cd *CompatDaemon) error {
		daemons = append(daemons, cd)
		return nil
	})

	if err != nil {
		return err
	}

	var sharedMode = false

	// Scan all the daemons if it is started as shared mode last time
	for _, d := range daemons {
		if d.ID == SharedNydusDaemonID {
			sharedMode = true
		}
	}

	for _, d := range daemons {
		var mp string
		var newDaemon *daemon.Daemon
		if sharedMode && d.ID == SharedNydusDaemonID {
			newDaemon = &daemon.Daemon{
				States: daemon.States{
					ID:         d.ID,
					ProcessID:  d.Pid,
					APISocket:  path.Join(d.SnapshotDir, "api.sock"),
					FsDriver:   d.FsDriver,
					Mountpoint: *d.RootMountPoint,
					LogDir:     d.LogDir,
					LogLevel:   d.LogLevel,
					// Shared daemon does not need config file when start
					ConfigDir: "",
				}}
		} else if !sharedMode {
			mp = *d.CustomMountPoint
			newDaemon = &daemon.Daemon{
				States: daemon.States{
					ID:         d.ID,
					ProcessID:  d.Pid,
					APISocket:  path.Join(d.SocketDir, "api.sock"),
					FsDriver:   d.FsDriver,
					Mountpoint: mp,
					LogDir:     d.LogDir,
					LogLevel:   d.LogLevel,
					ConfigDir:  d.ConfigDir,
				}}
		}

		var instance *daemon.Rafs
		if !sharedMode {
			instance = &daemon.Rafs{
				SnapshotID:  d.SnapshotID,
				ImageID:     d.ImageID,
				DaemonID:    d.ID,
				SnapshotDir: path.Join(d.SnapshotDir, d.SnapshotID),
				Mountpoint:  path.Join(d.SnapshotDir, d.SnapshotID, "mnt"),
			}
		} else if sharedMode && d.ID != SharedNydusDaemonID {
			instance = &daemon.Rafs{
				SnapshotID:  d.SnapshotID,
				ImageID:     d.ImageID,
				DaemonID:    SharedNydusDaemonID,
				SnapshotDir: path.Join(d.SnapshotDir, d.SnapshotID),
				Mountpoint:  path.Join(*d.RootMountPoint, d.SnapshotID),
			}
		}

		if newDaemon != nil {
			if err := db.SaveDaemon(context.TODO(), newDaemon); err != nil {
				return err
			}
		}

		if instance != nil {
			if err := db.AddInstance(context.TODO(), instance); err != nil {
				return err
			}
		}
	}

	return nil
}
