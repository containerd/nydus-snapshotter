/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package referrer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/remote"

	"github.com/containerd/nydus-snapshotter/pkg/remote/remotes"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// Containerd restricts the max size of manifest index to 8M, follow it.
const maxManifestIndexSize = 0x800000
const metadataNameInLayer = "image/image.boot"

type referrer struct {
	remote *remote.Remote
}

func newReferrer(keyChain *auth.PassKeyChain, insecure bool) *referrer {
	return &referrer{
		remote: remote.New(keyChain, insecure),
	}
}

// checkReferrer fetches the referrers and parses out the nydus
// image by specified manifest digest.
// it's using distribution list referrers API.
func (r *referrer) checkReferrer(ctx context.Context, ref string, manifestDigest digest.Digest) (*ocispec.Descriptor, error) {
	handle := func() (*ocispec.Descriptor, error) {
		// Create an new resolver to request.
		fetcher, err := r.remote.Fetcher(ctx, ref)
		if err != nil {
			return nil, errors.Wrap(err, "get fetcher")
		}

		// Fetch image referrers from remote registry.
		rc, _, err := fetcher.(remotes.ReferrersFetcher).FetchReferrers(ctx, manifestDigest)
		if err != nil {
			return nil, errors.Wrap(err, "fetch referrers")
		}
		defer rc.Close()

		// Parse image manifest list from referrers.
		var index ocispec.Index
		bytes, err := io.ReadAll(io.LimitReader(rc, maxManifestIndexSize))
		if err != nil {
			return nil, errors.Wrap(err, "read referrers")
		}
		if err := json.Unmarshal(bytes, &index); err != nil {
			return nil, errors.Wrap(err, "unmarshal referrers index")
		}
		if len(index.Manifests) == 0 {
			return nil, fmt.Errorf("empty referrer list")
		}

		// Prefer to fetch the last manifest and check if it is a nydus image.
		// TODO: should we search by matching ArtifactType?
		rc, err = fetcher.Fetch(ctx, index.Manifests[0])
		if err != nil {
			return nil, errors.Wrap(err, "fetch referrers")
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
	if err != nil && r.remote.RetryWithPlainHTTP(ref, err) {
		return handle()
	}

	return desc, err
}

// fetchMetadata fetches and unpacks nydus metadata file to specified path.
func (r *referrer) fetchMetadata(ctx context.Context, ref string, desc ocispec.Descriptor, metadataPath string) error {
	handle := func() error {
		// Create an new resolver to request.
		resolver := r.remote.Resolve(ctx, ref)
		fetcher, err := resolver.Fetcher(ctx, ref)
		if err != nil {
			return errors.Wrap(err, "get fetcher")
		}

		// Unpack nydus metadata file to specified path.
		rc, err := fetcher.Fetch(ctx, desc)
		if err != nil {
			return errors.Wrap(err, "fetch nydus metadata")
		}
		defer rc.Close()

		if err := remote.Unpack(rc, metadataNameInLayer, metadataPath); err != nil {
			os.Remove(metadataPath)
			return errors.Wrap(err, "unpack metadata from layer")
		}

		return nil
	}

	// TODO: check metafile already exists
	err := handle()
	if err != nil && r.remote.RetryWithPlainHTTP(ref, err) {
		return handle()
	}

	return err
}
