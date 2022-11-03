/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package cache

import (
	"context"
	"os"
	"path"
	"time"

	"github.com/pkg/errors"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/nydus-snapshotter/pkg/store"
)

const (
	chunkMapFileSuffix = ".chunk_map"
	metaFileSuffix     = ".blob.meta"
	// Blob cache is suffixed after nydus v2.1
	dataFileSuffix = ".blob.data"
)

type Manager struct {
	cacheDir string
	period   time.Duration
	eventCh  chan struct{}
	fsDriver string
}

type Opt struct {
	CacheDir string
	Period   time.Duration
	Database *store.Database
	FsDriver string
}

func NewManager(opt Opt) (*Manager, error) {
	// Ensure cache directory exists
	if err := os.MkdirAll(opt.CacheDir, 0755); err != nil {
		return nil, errors.Wrapf(err, "failed to create cache dir %s", opt.CacheDir)
	}

	eventCh := make(chan struct{})
	m := &Manager{
		cacheDir: opt.CacheDir,
		period:   opt.Period,
		eventCh:  eventCh,
		fsDriver: opt.FsDriver,
	}

	return m, nil
}

func (m *Manager) CacheDir() string {
	return m.cacheDir
}

// Report each blob disk usage
func (m *Manager) CacheUsage(ctx context.Context, blobID string) (snapshots.Usage, error) {
	var usage snapshots.Usage

	blobCachePath := path.Join(m.cacheDir, blobID)
	blobChunkMap := path.Join(m.cacheDir, blobID+chunkMapFileSuffix)
	blobMeta := path.Join(m.cacheDir, blobID+metaFileSuffix)

	stuffs := []string{blobCachePath, blobChunkMap, blobMeta}

	for _, f := range stuffs {
		du, err := fs.DiskUsage(ctx, f)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				log.L.Debugf("Cache %s does not exist", f)
				continue
			}
			return snapshots.Usage{}, err
		}
		usage.Add(snapshots.Usage(du))
	}

	return usage, nil
}

func (m *Manager) RemoveBlobCache(blobID string) error {
	blobCachePath := path.Join(m.cacheDir, blobID)
	blobCacheSuffixedPath := path.Join(m.cacheDir, blobID+dataFileSuffix)
	blobChunkMap := path.Join(m.cacheDir, blobID+chunkMapFileSuffix)
	blobMeta := path.Join(m.cacheDir, blobID+metaFileSuffix)

	// NOTE: Delete chunk bitmap file before data blob
	stuffs := []string{blobChunkMap, blobMeta, blobCachePath, blobCacheSuffixedPath}

	for _, f := range stuffs {
		err := os.Remove(f)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				log.L.Debugf("file %s doest not exist.", f)
				continue
			}
			return err
		}
	}
	return nil
}
