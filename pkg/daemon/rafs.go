/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemon

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"sync"

	"github.com/mohae/deepcopy"
	"github.com/pkg/errors"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"

	"github.com/containerd/nydus-snapshotter/config"
)

const (
	AnnoFsCacheDomainID string = "fscache.domainid"
	AnnoFsCacheID       string = "fscache.id"
)

type NewRafsOpt func(r *Rafs) error

func init() {
	// A set of rafs instances associates to a nydusd daemon or not
	RafsSet = rafsSet{instances: make(map[string]*Rafs)}
}

var RafsSet rafsSet

type rafsSet struct {
	mu        sync.Mutex
	instances map[string]*Rafs
}

func (rs *rafsSet) Lock() {
	rs.mu.Lock()
}

func (rs *rafsSet) Unlock() {
	rs.mu.Unlock()
}

func (rs *rafsSet) Add(r *Rafs) {
	rs.mu.Lock()
	rs.instances[r.SnapshotID] = r
	rs.mu.Unlock()
}

func (rs *rafsSet) Remove(snapshotID string) {
	rs.mu.Lock()
	delete(rs.instances, snapshotID)
	rs.mu.Unlock()
}

func (rs *rafsSet) Get(snapshotID string) *Rafs {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	return rs.instances[snapshotID]
}

func (rs *rafsSet) Len() int {
	rs.mu.Lock()
	len := len(rs.instances)
	rs.mu.Unlock()

	return len
}

func (rs *rafsSet) Head() *Rafs {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, v := range rs.instances {
		return v
	}

	return nil
}

func (rs *rafsSet) List() map[string]*Rafs {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	instances := deepcopy.Copy(rs.instances).(map[string]*Rafs)

	return instances
}

func (rs *rafsSet) ListUnlocked() map[string]*Rafs {
	return rs.instances
}

// The whole struct will be persisted
type Rafs struct {
	Seq uint64
	// Given by containerd
	SnapshotID string
	// Usually is the image reference
	ImageID     string
	DaemonID    string
	SnapshotDir string
	// 1. A host kernel EROFS mountpoint
	// 2. Absolute path to each rafs instance root directory.
	Mountpoint  string
	Annotations map[string]string
}

func NewRafs(snapshotID, imageID string) (*Rafs, error) {
	snapshotDir := path.Join(config.GetSnapshotsRootDir(), snapshotID)
	rafs := &Rafs{SnapshotID: snapshotID,
		ImageID:     imageID,
		SnapshotDir: snapshotDir,
		Annotations: make(map[string]string),
	}

	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		return nil, err
	}

	RafsSet.Add(rafs)

	return rafs, nil
}

func (r *Rafs) AddAnnotation(k, v string) {
	r.Annotations[k] = v
}

func (r *Rafs) SetMountpoint(mp string) {
	r.Mountpoint = mp
}

func (r *Rafs) GetSnapshotDir() string {
	return r.SnapshotDir
}

// Mountpoint for nydusd within single kernel mountpoint(FUSE mount). Each mountpoint
// is create by API based pseudo mount. `RootMountPoint` is real mountpoint
// where to perform the kernel mount.
// Nydusd API based mountpoint must start with "/", otherwise nydusd API server returns error.
func (r *Rafs) RelaMountpoint() string {
	return filepath.Join("/", r.SnapshotID)
}

// Reflects the path where the a rafs instance root stays. The
// root path could be a host kernel mountpoint when the instance
// is attached by API `POST /api/v1/mount?mountpoint=/` or nydusd mounts an instance directly when starting.
// Generally, we use this method to get the path as overlayfs lowerdir.
// The path includes container image rootfs.
func (r *Rafs) GetMountpoint() string {
	return r.Mountpoint
}

func (r *Rafs) BootstrapFile() (string, error) {
	// meta files are stored at <snapshot_id>/fs/image/image.boot
	bootstrap := filepath.Join(r.SnapshotDir, "fs", "image", "image.boot")
	_, err := os.Stat(bootstrap)
	if err == nil {
		return bootstrap, nil
	}

	if os.IsNotExist(err) {
		// check legacy location for backward compatibility
		bootstrap = filepath.Join(r.SnapshotDir, "fs", "image.boot")
		_, err = os.Stat(bootstrap)
		if err == nil {
			return bootstrap, nil
		}
	}

	return "", errors.Wrapf(errdefs.ErrNotFound, "bootstrap %s", bootstrap)
}

// Blob caches' chunk bitmap and meta headers are stored here.
func (r *Rafs) FscacheWorkDir() string {
	return filepath.Join(r.SnapshotDir, "fs")
}

func (d *Daemon) UmountAllInstances() error {
	if d.IsSharedDaemon() {
		d.Instances.Lock()
		defer d.Instances.Unlock()

		instances := d.Instances.ListUnlocked()

		for _, r := range instances {
			if err := d.SharedUmount(r); err != nil {
				return errors.Wrapf(err, "umount fs instance %s", r.SnapshotID)
			}
		}
	}

	return nil
}

func (d *Daemon) CloneInstances(src *Daemon) {
	src.Instances.Lock()
	defer src.Instances.Unlock()

	instances := src.Instances.ListUnlocked()

	d.Lock()
	defer d.Unlock()
	d.Instances.instances = instances
}

func (d *Daemon) UmountInstance(r *Rafs) error {
	if d.IsSharedDaemon() {
		if err := d.SharedUmount(r); err != nil {
			return errors.Wrapf(err, "umount fs instance %s", r.SnapshotID)
		}
	}

	return nil
}

// Daemon must be started and reach RUNNING state before call this method
func (d *Daemon) RecoveredMountInstances() error {
	if d.IsSharedDaemon() {
		d.Instances.Lock()
		defer d.Instances.Unlock()

		instances := make([]*Rafs, 0, 16)
		for _, r := range d.Instances.ListUnlocked() {
			instances = append(instances, r)
		}

		sort.Slice(instances, func(i, j int) bool {
			return instances[i].Seq < instances[j].Seq
		})

		for _, i := range instances {
			log.L.Infof("Recovered mount instance %s", i.SnapshotID)
			if err := d.SharedMount(i); err != nil {
				return err
			}
		}
	}

	return nil
}
