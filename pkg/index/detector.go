/*
 * Copyright (c) 2025. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package index

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/converter"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/remote"
)

// Containerd restricts the max size of manifest index to 8M, follow it.
const maxManifestIndexSize = 0x800000

type detector struct {
	remote *remote.Remote
}

func newDetector(keyChain *auth.PassKeyChain, insecure bool) *detector {
	return &detector{
		remote: remote.New(keyChain, insecure),
	}
}

// checkIndexAlternative attempts to find a nydus alternative manifest
// within an OCI index manifest for the specified manifest digest.
func (d *detector) checkIndexAlternative(ctx context.Context, ref string, manifestDigest digest.Digest) (*ocispec.Descriptor, error) {
	handle := func() (*ocispec.Descriptor, error) {
		// Create a new fetcher to request.
		fetcher, err := d.remote.Fetcher(ctx, ref)
		if err != nil {
			return nil, errors.Wrap(err, "get fetcher")
		}

		resolver := d.remote.Resolve(ctx, ref)
		_, desc, err := resolver.Resolve(ctx, ref)
		if err != nil {
			return nil, errors.Wrapf(err, "resolve reference %s", ref)
		}

		rc, err := fetcher.Fetch(ctx, desc)
		if err != nil {
			return nil, errors.Wrap(err, "fetch index manifest")
		}
		defer rc.Close()

		// Parse image manifest list from index.
		var index ocispec.Index
		bytes, err := io.ReadAll(io.LimitReader(rc, maxManifestIndexSize))
		if err != nil {
			return nil, errors.Wrap(err, "read index manifest")
		}
		if err := json.Unmarshal(bytes, &index); err != nil {
			return nil, errors.Wrap(err, "unmarshal index manifest")
		}

		nydusDesc, err := d.findNydusManifestInIndex(index, manifestDigest)
		if err != nil {
			return nil, err
		}

		rc, err = fetcher.Fetch(ctx, *nydusDesc)
		if err != nil {
			return nil, errors.Wrap(err, "fetch nydus image manifest")
		}
		defer rc.Close()

		var manifest ocispec.Manifest
		bytes, err = io.ReadAll(rc)
		if err != nil {
			return nil, errors.Wrap(err, "read manifest")
		}
		if err := json.Unmarshal(bytes, &manifest); err != nil {
			return nil, errors.Wrap(err, "unmarshal manifest")
		}
		if len(manifest.Layers) < 1 {
			return nil, fmt.Errorf("invalid manifest")
		}
		metaLayer := manifest.Layers[len(manifest.Layers)-1]
		if !label.IsNydusMetaLayer(metaLayer.Annotations) {
			return nil, fmt.Errorf("invalid nydus manifest")
		}

		return &metaLayer, nil
	}

	desc, err := handle()
	if err != nil && d.remote.RetryWithPlainHTTP(ref, err) {
		return handle()
	}

	return desc, err
}

// findNydusManifestInIndex finds a nydus alternative manifest within an OCI index
// for the specified manifest digest. It returns the nydus manifest descriptor.
func (d *detector) findNydusManifestInIndex(index ocispec.Index, originalDigest digest.Digest) (*ocispec.Descriptor, error) {
	var originalDesc *ocispec.Descriptor
	for _, manifest := range index.Manifests {
		if manifest.Digest == originalDigest {
			originalDesc = &manifest
			break
		}
	}
	if originalDesc == nil {
		return nil, fmt.Errorf("original manifest %s not found in index", originalDigest)
	}

	pMatcher := platforms.NewMatcher(*originalDesc.Platform)
	for _, manifest := range index.Manifests {
		if pMatcher.Match(*manifest.Platform) &&
			(d.hasNydusFeatures(manifest.Platform) || d.hasNydusArtifactType(&manifest)) {
			return &manifest, nil
		}
	}

	return nil, fmt.Errorf("no nydus alternative found in index for %s", originalDigest)
}

// hasNydusFeatures checks if the platform descriptor contains nydus features.
func (d *detector) hasNydusFeatures(platform *ocispec.Platform) bool {
	if platform == nil {
		return false
	}

	return slices.Contains(platform.OSFeatures, converter.ManifestOSFeatureNydus)
}

// hasNydusArtifactType checks if the descriptor is of nydus artifact type.
func (d *detector) hasNydusArtifactType(desc *ocispec.Descriptor) bool {
	if desc == nil {
		return false
	}
	return desc.ArtifactType == converter.ManifestArtifactTypeNydus
}

// fetchMetadata fetches and unpacks nydus metadata file to specified path.
func (d *detector) fetchMetadata(ctx context.Context, ref string, desc ocispec.Descriptor, metadataPath string) error {
	handle := func() error {
		resolver := d.remote.Resolve(ctx, ref)
		fetcher, err := resolver.Fetcher(ctx, ref)
		if err != nil {
			return errors.Wrap(err, "get fetcher")
		}

		rc, err := fetcher.Fetch(ctx, desc)
		if err != nil {
			return errors.Wrap(err, "fetch nydus metadata")
		}
		defer rc.Close()

		// Unpack nydus metadata file to specified path.
		if err := remote.Unpack(rc, converter.BootstrapFileNameInLayer, metadataPath); err != nil {
			os.Remove(metadataPath)
			return errors.Wrap(err, "unpack metadata from layer")
		}

		return nil
	}

	err := handle()
	if err != nil && d.remote.RetryWithPlainHTTP(ref, err) {
		return handle()
	}

	return err
}
