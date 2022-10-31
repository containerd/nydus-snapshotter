/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package blob

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/nydus-snapshotter/pkg/resolve"
	"github.com/containerd/nydus-snapshotter/pkg/utils/registry"
)

type BlobManager struct {
	blobDir   string
	eventChan chan string
	resolver  *resolve.Resolver
}

func NewBlobManager(blobDir string, resolver *resolve.Resolver) *BlobManager {
	return &BlobManager{
		blobDir: blobDir,
		// TODO(tianqian.zyf): Remove hardcode chan buffer
		eventChan: make(chan string, 8),
		resolver:  resolver,
	}
}

func getBlobPath(dir string, blobDigest string) (string, error) {
	digest, err := digest.Parse(blobDigest)
	if err != nil {
		return "", errors.Wrapf(err, "invalid layer digest %s", blobDigest)
	}
	return filepath.Join(dir, digest.Encoded()), nil
}

func (b *BlobManager) Run(ctx context.Context) error {
	log.G(ctx).Info("blob manager goroutine start...")
	for {
		select {
		case id := <-b.eventChan:
			err := b.cleanupBlob(id)
			if err != nil {
				log.G(ctx).Warnf("delete blob %s failed", id)
			} else {
				log.G(ctx).Infof("delete blob %s success", id)
			}
		case <-ctx.Done():
			log.G(ctx).Infof("exit from BlobManger")
			return ctx.Err()
		}
	}
}

func (b *BlobManager) GetBlobDir() string {
	return b.blobDir
}

func (b *BlobManager) cleanupBlob(id string) error {
	return os.Remove(filepath.Join(b.blobDir, id))
}

func (b *BlobManager) decodeID(id string) (string, error) {
	digest, err := digest.Parse(id)
	if err != nil {
		return "", errors.Wrapf(err, "invalid blob layer digest %s", id)
	}
	return digest.Encoded(), nil
}

func (b *BlobManager) Remove(id string, async bool) error {
	id, err := b.decodeID(id)
	if err != nil {
		return err
	}
	if async {
		b.eventChan <- id
		return nil
	}
	return b.cleanupBlob(id)
}

func (b *BlobManager) CleanupBlobLayer(ctx context.Context, blobDigest string, async bool) error {
	return b.Remove(blobDigest, async)
}

// Download blobs and bootstrap in nydus-snapshotter for preheating container image usage. It has to
// enable blobs manager when start nydus-snapshotter
func (b *BlobManager) PrepareBlobLayer(snapshot storage.Snapshot, labels map[string]string) error {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		log.L.Infof("total nydus prepare data layer duration %s", duration)
	}()

	ref, layerDigest := registry.ParseLabels(labels)
	if ref == "" || layerDigest == "" {
		return fmt.Errorf("can not find ref and digest from label %+v", labels)
	}
	blobPath, err := getBlobPath(b.GetBlobDir(), layerDigest)
	if err != nil {
		return errors.Wrap(err, "failed to get blob path")
	}
	_, err = os.Stat(blobPath)
	if err == nil {
		log.L.Debugf("%s blob layer already exists", blobPath)
		return nil
	} else if !os.IsNotExist(err) {
		return errors.Wrap(err, "Unexpected error, we can't handle it")
	}

	readerCloser, err := b.resolver.Resolve(ref, layerDigest, labels)
	if err != nil {
		return errors.Wrapf(err, "failed to resolve from ref %s, digest %s", ref, layerDigest)
	}
	defer readerCloser.Close()

	blobFile, err := os.CreateTemp(b.GetBlobDir(), "downloading-")
	if err != nil {
		return errors.Wrap(err, "create temp file for downloading blob")
	}
	defer func() {
		if err != nil {
			os.Remove(blobFile.Name())
		}
		blobFile.Close()
	}()

	_, err = io.Copy(blobFile, readerCloser)
	if err != nil {
		return errors.Wrap(err, "write blob to local file")
	}
	err = os.Rename(blobFile.Name(), blobPath)
	if err != nil {
		return errors.Wrap(err, "rename temp file as blob file")
	}
	os.Chmod(blobFile.Name(), 0440)

	return nil
}
