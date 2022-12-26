/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type LocalFSBackend struct {
	dir       string
	forcePush bool
}

func newLocalFSBackend(rawConfig []byte, forcePush bool) (*LocalFSBackend, error) {
	var configMap map[string]string
	if err := json.Unmarshal(rawConfig, &configMap); err != nil {
		return nil, errors.Wrap(err, "parse LocalFS storage backend configuration")
	}

	dir, ok := configMap["dir"]
	if !ok {
		return nil, fmt.Errorf("no `dir` option is specified")
	}

	return &LocalFSBackend{
		dir:       dir,
		forcePush: forcePush,
	}, nil
}

func (b *LocalFSBackend) dstPath(blobID string) string {
	return path.Join(b.dir, blobID)
}

func (b *LocalFSBackend) Push(ctx context.Context, cs content.Store, desc ocispec.Descriptor) error {
	if _, err := b.Check(desc.Digest); err == nil && !b.forcePush {
		return nil
	}

	if err := os.MkdirAll(b.dir, 0755); err != nil {
		return errors.Wrap(err, "create directory in localfs backend")
	}

	ra, err := cs.ReaderAt(ctx, desc)
	if err != nil {
		return errors.Wrap(err, "get reader from content store")
	}
	defer ra.Close()

	blobID := desc.Digest.Hex()
	dstPath := b.dstPath(blobID)

	dstFile, err := os.Create(dstPath)
	if err != nil {
		return errors.Wrapf(err, "create destination file: %s", dstPath)
	}
	defer dstFile.Close()

	sr := io.NewSectionReader(ra, 0, ra.Size())
	if _, err := io.Copy(dstFile, sr); err != nil {
		return errors.Wrapf(err, "copy blob to %s", dstPath)
	}

	return nil
}

func (b *LocalFSBackend) Check(blobDigest digest.Digest) (string, error) {
	dstPath := b.dstPath(blobDigest.Hex())

	info, err := os.Stat(dstPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errdefs.ErrNotFound
		}
		return "", err
	}

	if !info.IsDir() {
		return dstPath, nil
	}

	return "", errdefs.ErrNotFound
}

func (b *LocalFSBackend) Type() string {
	return BackendTypeLocalFS
}
