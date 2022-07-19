/* SPDX-License-Identifier: Apache-2.0 */
/* Copyright (c) 2022. Alibaba Cloud, Ant Group. All rights reserved. */

package converter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

const ManifestOSFeatureNydus = "nydus.remoteimage.v1"

func GetManifestByDigest(ctx context.Context, provider content.Provider, target digest.Digest, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
	switch desc.MediaType {
	case images.MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest:
		if desc.Digest == target {
			return &desc, nil
		}
	case images.MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
		p, err := content.ReadBlob(ctx, provider, desc)
		if err != nil {
			return nil, err
		}

		var index ocispec.Index
		if err := json.Unmarshal(p, &index); err != nil {
			return nil, errors.Wrap(err, "unmarshal image index")
		}

		for _, idesc := range index.Manifests {
			if idesc.Digest == target {
				return &idesc, nil
			}
		}
	}

	return nil, fmt.Errorf("can not get manifest descriptor by digest %+v from descriptor %+v", target, desc)
}

func GetLayersByManifestDescriptor(ctx context.Context, provider content.Provider, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	if desc.MediaType != images.MediaTypeDockerSchema2Manifest && desc.MediaType != ocispec.MediaTypeImageManifest {
		return nil, fmt.Errorf("descriptor %+v is not manifest descriptor", desc)
	}

	p, err := content.ReadBlob(ctx, provider, desc)
	if err != nil {
		return nil, err
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(p, &manifest); err != nil {
		return nil, errors.Wrap(err, "unmarshal image manifest")
	}

	return manifest.Layers, nil
}
