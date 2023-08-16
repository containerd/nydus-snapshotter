/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemon

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
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
	// TODO
	// A set of RAFS filesystem instances associated with a nydusd daemon.
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

func (rs *rafsSet) ListLocked() map[string]*Rafs {
	return rs.instances
}

// The whole struct will be persisted
type Rafs struct {
	Seq uint64
	// Usually is the image reference
	ImageID  string
	DaemonID string
	FsDriver string
	// Given by containerd
	SnapshotID  string
	SnapshotDir string
	// 1. A host kernel EROFS mountpoint
	// 2. Absolute path to each rafs instance root directory.
	Mountpoint  string
	Annotations map[string]string
}

func NewRafs(snapshotID, imageID, fsDriver string) (*Rafs, error) {
	snapshotDir := path.Join(config.GetSnapshotsRootDir(), snapshotID)
	rafs := &Rafs{
		FsDriver:    fsDriver,
		ImageID:     imageID,
		SnapshotID:  snapshotID,
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

func (r *Rafs) GetSnapshotDir() string {
	return r.SnapshotDir
}

func (r *Rafs) GetFsDriver() string {
	if r.FsDriver != "" {
		return r.FsDriver
	}

	return config.GetFsDriver()
}

// Blob caches' chunk bitmap and meta headers are stored here.
func (r *Rafs) FscacheWorkDir() string {
	return filepath.Join(r.SnapshotDir, "fs")
}

func (r *Rafs) SetMountpoint(mp string) {
	r.Mountpoint = mp
}

// Get top level mount point for the RAFS instance:
//   - FUSE with dedicated mode: the FUSE filesystem mount point, the RAFS filesystem is directly
//     mounted at the mount point.
//   - FUSE with shared mode: the FUSE filesystem mount point, the RAFS filesystem is mounted
//     at a subdirectory under the mount point.
//   - EROFS/fscache: the EROFS filesystem mount point.
func (r *Rafs) GetMountpoint() string {
	return r.Mountpoint
}

// Get the sub-directory under a FUSE mount point to mount a RAFS instance.
// For a nydusd daemon in shared mode, one or more RAFS filesystem instances can be mounted
// to sub-directories of the FUSE filesystem. This method returns the subdirectory for a
// RAFS filesystem instance.
func (r *Rafs) RelaMountpoint() string {
	return filepath.Join("/", r.SnapshotID)
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

func buildDeduplicationCommand(bootstrapPath, configPath, nydusImagePath string) *exec.Cmd {
	args := []string{
		"dedup",
		"--bootstrap", bootstrapPath,
		"--config", configPath,
	}

	log.L.Infof("start bootstrap deduplication: %s %s", nydusImagePath, strings.Join(args, " "))

	cmd := exec.Command(nydusImagePath, args...)

	return cmd

}

func (r *Rafs) DeduplicateBootstrap(bootstrapPath, configPath string) (string, error) {
	nydusImagePath, err := exec.LookPath("nydus-image")
	if err != nil {
		return "", err
	}

	cmd := buildDeduplicationCommand(bootstrapPath, configPath, nydusImagePath)
	_, err = cmd.CombinedOutput()
	if err != nil {
		return "", err
	}

	_, err = os.Stat(bootstrapPath + ".dedup")
	if err != nil {
		return "", err
	}
	bootstrapPath += ".dedup"
	return bootstrapPath, err
}
