/*
 * Copyright (c) 2025. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package filesystem

import (
	"context"
	"fmt"

	snpkg "github.com/containerd/containerd/v2/pkg/snapshotters"
	"github.com/containerd/log"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

func (fs *Filesystem) IndexDetectEnabled() bool {
	return fs.indexMgr != nil
}

// CheckIndexAlternative attempts to find a nydus alternative manifest in the original OCI index manifest
func (fs *Filesystem) CheckIndexAlternative(ctx context.Context, labels map[string]string) bool {
	if !fs.IndexDetectEnabled() {
		return false
	}

	ref, ok := labels[snpkg.TargetRefLabel]
	if !ok {
		return false
	}

	manifestDigest := digest.Digest(labels[snpkg.TargetManifestDigestLabel])
	if manifestDigest.Validate() != nil {
		return false
	}

	log.G(ctx).WithField("ref", ref).WithField("digest", manifestDigest.String()).Debug("attempting index-based nydus detection")
	if _, err := fs.indexMgr.CheckIndexAlternative(ctx, ref, manifestDigest); err != nil {
		return false
	}

	return true
}

// TryFetchMetadataFromIndex attempts to fetch metadata from the nydus index alternative
func (fs *Filesystem) TryFetchMetadataFromIndex(ctx context.Context, labels map[string]string, metadataPath string) error {
	ref, ok := labels[snpkg.TargetRefLabel]
	if !ok {
		return fmt.Errorf("empty label %s", snpkg.TargetRefLabel)
	}

	manifestDigest := digest.Digest(labels[snpkg.TargetManifestDigestLabel])
	if err := manifestDigest.Validate(); err != nil {
		return fmt.Errorf("invalid label %s=%s", snpkg.TargetManifestDigestLabel, manifestDigest)
	}

	if err := fs.indexMgr.TryFetchMetadata(ctx, ref, manifestDigest, metadataPath); err != nil {
		return errors.Wrap(err, "try fetch metadata")
	}

	return nil
}
