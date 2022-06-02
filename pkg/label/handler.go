/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package label

import (
	"context"
	"fmt"
	"strings"

	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/labels"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func AppendLabelsHandler(ref string) func(f images.Handler) images.Handler {
	return func(f images.Handler) images.Handler {
		return images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
			children, err := f.Handle(ctx, desc)
			if err != nil {
				return nil, err
			}
			switch desc.MediaType {
			case ocispec.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest:
				for i := range children {
					c := &children[i]
					if images.IsLayerType(c.MediaType) {
						if c.Annotations == nil {
							c.Annotations = make(map[string]string)
						}
						c.Annotations[CRIImageRef] = ref
						c.Annotations[CRILayerDigest] = c.Digest.String()
						var layers string
						for _, l := range children[i:] {
							if images.IsLayerType(l.MediaType) || l.MediaType == NydusMetaLayer || l.MediaType == NydusDataLayer {
								ls := fmt.Sprintf("%s,", l.Digest.String())
								// This avoids the label hits the size limitation.
								// Skipping layers is allowed here and only affects performance.
								if err := labels.Validate(CRIImageLayers, layers+ls); err != nil {
									break
								}
								layers += ls
							}
						}
						c.Annotations[CRIImageLayers] = strings.TrimSuffix(layers, ",")
					}
				}
			}
			return children, nil
		})
	}
}
