/*
 * Copyright (c) 2021. Ant Financial. All rights reserved.
  * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
*/

package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"

	"github.com/pkg/errors"
	bolt "go.etcd.io/bbolt"
)

const (
	databaseFileName = "nydus.db"
)

// Bucket names:
// Buckets hierarchy:
//	- v1:
//		- daemons
//		- instances

var (
	v1RootBucket = []byte("v1")
	// Nydusd daemon instances.
	// A daemon may host one (dedicated mode) or more (shared mode) RAFS filesystem instances.
	versionKey    = []byte("version")
	daemonsBucket = []byte("daemons")
	// RAFS filesystem instances.
	// A RAFS filesystem may have associated daemon or not.
	instancesBucket = []byte("instances")
)

// Database keeps infos that need to survive among snapshotter restart
type Database struct {
	db *bolt.DB
}

// NewDatabase creates a new or open existing database file
func NewDatabase(rootDir string) (*Database, error) {
	f := filepath.Join(rootDir, databaseFileName)
	if err := ensureDirectory(filepath.Dir(f)); err != nil {
		return nil, err
	}

	opts := bolt.Options{Timeout: time.Second * 4}

	db, err := bolt.Open(f, 0600, &opts)
	if err != nil {
		return nil, err
	}
	d := &Database{db: db}
	if err := d.initDatabase(); err != nil {
		return nil, errors.Wrap(err, "failed to initialize database")
	}
	return d, nil
}

func ensureDirectory(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return os.MkdirAll(dir, 0700)
	}

	return nil
}

func getDaemonsBucket(tx *bolt.Tx) *bolt.Bucket {
	bucket := tx.Bucket(v1RootBucket)
	return bucket.Bucket(daemonsBucket)
}

func getInstancesBucket(tx *bolt.Tx) *bolt.Bucket {
	bucket := tx.Bucket(v1RootBucket)
	return bucket.Bucket(instancesBucket)
}

func updateObject(bucket *bolt.Bucket, key string, obj interface{}) error {
	keyBytes := []byte(key)

	value, err := json.Marshal(obj)
	if err != nil {
		return errors.Wrapf(err, "marshall key %s", key)
	}

	if err := bucket.Put(keyBytes, value); err != nil {
		return errors.Wrapf(err, "put key %s", key)
	}

	return nil
}

func putObject(bucket *bolt.Bucket, key string, obj interface{}) error {
	keyBytes := []byte(key)

	if bucket.Get(keyBytes) != nil {
		return errors.Errorf("object with key %q already exists", key)
	}

	value, err := json.Marshal(obj)
	if err != nil {
		return errors.Wrapf(err, "marshall %s", key)
	}

	if err := bucket.Put(keyBytes, value); err != nil {
		return errors.Wrapf(err, "put key %s", key)
	}

	return nil
}

// A basic wrapper to retrieve a object from bucket.
func getObject(bucket *bolt.Bucket, key string, obj interface{}) error {
	if obj == nil {
		return errdefs.ErrInvalidArgument
	}

	value := bucket.Get([]byte(key))
	if value == nil {
		return errdefs.ErrNotFound
	}

	if err := json.Unmarshal(value, obj); err != nil {
		return errors.Wrapf(err, "unmarshall %s", key)
	}

	return nil
}

func (db *Database) initDatabase() error {
	var notV1 = false
	var version string
	err := db.db.Update(func(tx *bolt.Tx) error {

		bk := tx.Bucket(v1RootBucket)
		if bk == nil {
			notV1 = true
		}

		// Must create v1 bucket
		bk, err := tx.CreateBucketIfNotExists(v1RootBucket)
		if err != nil {
			return err
		}

		if _, err := bk.CreateBucketIfNotExists(daemonsBucket); err != nil {
			return errors.Wrapf(err, "bucket %s", daemonsBucket)
		}

		if _, err := bk.CreateBucketIfNotExists(instancesBucket); err != nil {
			return errors.Wrapf(err, "bucket %s", instancesBucket)
		}

		if val := bk.Get(versionKey); val == nil {
			version = "v1.0"
		} else {
			version = string(val)
		}

		return nil
	})
	if err != nil {
		return err
	}

	if notV1 {
		if err := db.tryTranslateRecords(); err != nil && !errors.Is(err, errdefs.ErrNotFound) {
			return errors.Wrapf(err, "convert old database")
		}
	}

	if version == "v1.0" {
		if err := db.tryUpgradeRecords(version); err != nil && !errors.Is(err, errdefs.ErrNotFound) {
			return errors.Wrapf(err, "convert old database")
		}
	}

	return nil
}

func (db *Database) Close() error {
	err := db.db.Close()
	if err != nil {
		return errors.Wrapf(err, "failed to close boltdb")
	}

	return nil
}

func (db *Database) SaveDaemon(ctx context.Context, d *daemon.Daemon) error {
	return db.db.Update(func(tx *bolt.Tx) error {
		bucket := getDaemonsBucket(tx)
		var existing daemon.States
		if err := getObject(bucket, d.ID(), &existing); err == nil {
			return errdefs.ErrAlreadyExists
		}
		return putObject(bucket, d.ID(), d.States)
	})
}

func (db *Database) UpdateDaemon(ctx context.Context, d *daemon.Daemon) error {
	return db.db.Update(func(tx *bolt.Tx) error {
		bucket := getDaemonsBucket(tx)

		var existing daemon.States
		if err := getObject(bucket, d.ID(), &existing); err != nil {
			return err
		}

		return updateObject(bucket, d.ID(), d.States)
	})
}

func (db *Database) DeleteDaemon(ctx context.Context, id string) error {
	return db.db.Update(func(tx *bolt.Tx) error {
		bucket := getDaemonsBucket(tx)

		if err := bucket.Delete([]byte(id)); err != nil {
			return errors.Wrapf(err, "delete daemon %s", id)
		}

		return nil
	})
}

// Cleanup deletes all daemon records
func (db *Database) CleanupDaemons(ctx context.Context) error {
	return db.db.Update(func(tx *bolt.Tx) error {
		bucket := getDaemonsBucket(tx)

		return bucket.ForEach(func(k, _ []byte) error {
			return bucket.Delete(k)
		})
	})
}

func (db *Database) WalkDaemons(ctx context.Context, cb func(info *daemon.States) error) error {
	return db.db.View(func(tx *bolt.Tx) error {
		bucket := getDaemonsBucket(tx)

		return bucket.ForEach(func(key, value []byte) error {
			states := &daemon.States{}

			if err := json.Unmarshal(value, states); err != nil {
				return errors.Wrapf(err, "unmarshal %s", key)
			}

			return cb(states)
		})
	})
}

// WalkDaemons iterates all daemon records and invoke callback on each
func (db *Database) WalkRafsInstances(ctx context.Context, cb func(r *rafs.Rafs) error) error {
	return db.db.View(func(tx *bolt.Tx) error {
		bucket := getInstancesBucket(tx)

		return bucket.ForEach(func(key, value []byte) error {
			instance := &rafs.Rafs{}

			if err := json.Unmarshal(value, instance); err != nil {
				return errors.Wrapf(err, "unmarshal %s", key)
			}

			return cb(instance)
		})
	})
}

func (db *Database) AddRafsInstance(ctx context.Context, instance *rafs.Rafs) error {
	return db.db.Update(func(tx *bolt.Tx) error {
		bucket := getInstancesBucket(tx)

		return putObject(bucket, instance.SnapshotID, instance)
	})
}

func (db *Database) DeleteRafsInstance(ctx context.Context, snapshotID string) error {
	return db.db.Update(func(tx *bolt.Tx) error {
		bucket := getInstancesBucket(tx)

		if err := bucket.Delete([]byte(snapshotID)); err != nil {
			return errors.Wrapf(err, "instance snapshot ID %s", snapshotID)
		}

		return nil
	})
}

func (db *Database) NextInstanceSeq() (uint64, error) {
	tx, err := db.db.Begin(true)
	if err != nil {
		return 0, errors.New("failed to start transaction")
	}

	defer func() {
		if err != nil {
			if err := tx.Rollback(); err != nil {
				log.L.WithError(err).Errorf("Rollback error when getting next sequence")
			}
		}
	}()

	bk := getInstancesBucket(tx)
	if bk == nil {
		return 0, errdefs.ErrNotFound
	}

	seq, err := bk.NextSequence()
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return seq, nil
}
