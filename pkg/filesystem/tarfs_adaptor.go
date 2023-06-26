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

func (fs *Filesystem) PrepareTarfsLayer(ctx context.Context, labels map[string]string, snapshotID, upperDirPath string) error {
	ref, ok := labels[snpkg.TargetRefLabel]
	if !ok {
		return errors.Errorf("not found image reference label")
	}
	layerDigest := digest.Digest(labels[snpkg.TargetLayerDigestLabel])
	if layerDigest.Validate() != nil {
		return errors.Errorf("not found layer digest label")
	}
	manifestDigest := digest.Digest(labels[snpkg.TargetManifestDigestLabel])
	if manifestDigest.Validate() != nil {
		return errors.Errorf("not found manifest digest label")
	}

	ok, err := fs.tarfsMgr.CheckTarfsHintAnnotation(ctx, ref, manifestDigest)
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

	if err := fs.tarfsMgr.PrepareLayer(snapshotID, ref, manifestDigest, layerDigest, upperDirPath); err != nil {
		log.L.WithError(err).Errorf("async prepare tarfs layer of snapshot ID %s", snapshotID)
	}
	if limiter != nil {
		limiter.Release(1)
	}

	return nil
}

func (fs *Filesystem) ExportBlockData(s storage.Snapshot, perLayer bool, labels map[string]string,
	storageLocater func(string) string) ([]string, error) {
	return fs.tarfsMgr.ExportBlockData(s, perLayer, labels, storageLocater)
}

func (fs *Filesystem) MergeTarfsLayers(s storage.Snapshot, storageLocater func(string) string) error {
	return fs.tarfsMgr.MergeLayers(s, storageLocater)
}

func (fs *Filesystem) DetachTarfsLayer(snapshotID string) error {
	return fs.tarfsMgr.DetachLayer(snapshotID)
}

func (fs *Filesystem) IsTarfsLayer(snapshotID string) bool {
	return fs.tarfsMgr.IsTarfsLayer(snapshotID)
}

func (fs *Filesystem) IsMountedTarfsLayer(snapshotID string) bool {
	return fs.tarfsMgr.IsMountedTarfsLayer(snapshotID)
}
