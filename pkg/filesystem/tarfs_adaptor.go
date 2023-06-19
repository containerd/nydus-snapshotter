/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package filesystem

import (
	"context"

	"github.com/containerd/containerd/log"
	snpkg "github.com/containerd/containerd/pkg/snapshotters"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

func (fs *Filesystem) TarfsEnabled() bool {
	return fs.tarfsMgr != nil
}

func (fs *Filesystem) PrepareTarfsLayer(ctx context.Context, labels map[string]string, snapshotID, storagePath string) error {
	ref, ok := labels[snpkg.TargetRefLabel]
	if !ok {
		return errors.Errorf("not found image reference lable")
	}
	layerDigest := digest.Digest(labels[snpkg.TargetLayerDigestLabel])
	if layerDigest.Validate() != nil {
		return errors.Errorf("not found manifest digest lable")
	}
	manifest := digest.Digest(labels[snpkg.TargetManifestDigestLabel])
	if manifest.Validate() != nil {
		return errors.Errorf("not found manifest digest lable")
	}

	ok, err := fs.tarfsMgr.CheckTarfsHintAnnotation(ctx, ref, manifest)
	if err != nil {
		return errors.Wrapf(err, "check tarfs hint annotaion")
	}
	if !ok {
		return errors.Errorf("this image is not recommended for tarfs")
	}

	limiter := fs.tarfsMgr.GetConcurrentLimiter(ref)
	if limiter != nil {
		if err := limiter.Acquire(context.Background(), 1); err != nil {
			return errors.Wrapf(err, "concurrent limiter acquire")
		}
	}

	go func() {
		if err := fs.tarfsMgr.PrepareLayer(snapshotID, ref, manifest, layerDigest, storagePath); err != nil {
			log.L.WithError(err).Errorf("async prepare Tarfs layer of snapshot ID %s", snapshotID)
		}
		if limiter != nil {
			limiter.Release(1)
		}
	}()
	return nil
}

func (fs *Filesystem) MergeTarfsLayers(s storage.Snapshot, storageLocater func(string) string) error {
	return fs.tarfsMgr.MergeLayers(s, storageLocater)
}

func (fs *Filesystem) DetachTarfsLayer(snapshotID string) error {
	return fs.tarfsMgr.DetachLayer(snapshotID)
}

func (fs *Filesystem) IsTarfsLayer(snapshotID string) bool {
	return fs.tarfsMgr.CheckTarfsLayer(snapshotID, false)
}

func (fs *Filesystem) IsMergedTarfsLayer(snapshotID string) bool {
	return fs.tarfsMgr.CheckTarfsLayer(snapshotID, true)
}
