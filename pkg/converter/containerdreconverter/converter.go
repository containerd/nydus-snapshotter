/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

// Package converter provides image converter
package containerdreconverter

import (
	"context"

	"github.com/containerd/containerd/platforms"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	ManifestOSFeatureNydus   = "nydus.remoteimage.v1"
	MediaTypeNydusBlob       = "application/vnd.oci.image.layer.nydus.blob.v1"
	BootstrapFileNameInLayer = "image/image.boot"

	ManifestNydusCache = "containerd.io/snapshot/nydus-cache"

	LayerAnnotationFSVersion          = "containerd.io/snapshot/nydus-fs-version"
	LayerAnnotationNydusBlob          = "containerd.io/snapshot/nydus-blob"
	LayerAnnotationNydusBlobDigest    = "containerd.io/snapshot/nydus-blob-digest"
	LayerAnnotationNydusBlobSize      = "containerd.io/snapshot/nydus-blob-size"
	LayerAnnotationNydusBootstrap     = "containerd.io/snapshot/nydus-bootstrap"
	LayerAnnotationNydusSourceChainID = "containerd.io/snapshot/nydus-source-chainid"
	LayerAnnotationNydusEncryptedBlob = "containerd.io/snapshot/nydus-encrypted-blob"
	LayerAnnotationNydusSourceDigest  = "containerd.io/snapshot/nydus-source-digest"
	LayerAnnotationNydusTargetDigest  = "containerd.io/snapshot/nydus-target-digest"

	LayerAnnotationNydusReferenceBlobIDs = "containerd.io/snapshot/nydus-reference-blob-ids"

	LayerAnnotationUncompressed = "containerd.io/uncompressed"
)

type convertOpts struct {
	layerConvertFunc ConvertFunc
	docker2oci       bool
	indexConvertFunc ConvertFunc
	platformMC       platforms.MatchComparer
}

// Opt is an option for Convert()
type Opt func(*convertOpts) error

// WithIndexConvertFunc specifies the function that converts manifests and index (manifest lists).
// Defaults to DefaultIndexConvertFunc.
func WithIndexConvertFunc(fn ConvertFunc) Opt {
	return func(copts *convertOpts) error {
		copts.indexConvertFunc = fn
		return nil
	}
}

// Convert converts an image.
func ReConvert(ctx context.Context, client containerd.Client, dstRef, srcRef string, opts ...Opt) (*images.Image, error) {
	var copts convertOpts
	for _, o := range opts {
		if err := o(&copts); err != nil {
			return nil, err
		}
	}
	if copts.platformMC == nil {
		copts.platformMC = platforms.All
	}
	if copts.indexConvertFunc == nil {
		copts.indexConvertFunc = DefaultIndexConvertFunc(copts.layerConvertFunc, copts.docker2oci, copts.platformMC)
	}

	ctx, done, err := client.WithLease(ctx)
	if err != nil {
		return nil, err
	}
	defer done(ctx)

	cs := client.ContentStore()
	is := client.ImageService()
	srcImg, err := is.Get(ctx, srcRef)
	if err != nil {
		return nil, err
	}
	dstDesc, err := copts.indexConvertFunc(ctx, cs, srcImg.Target)
	if err != nil {
		return nil, err
	}
	dstImg := srcImg
	dstImg.Name = dstRef
	if dstDesc != nil {
		dstImg.Target = *dstDesc
	}
	var res images.Image
	if dstRef != srcRef {
		_ = is.Delete(ctx, dstRef)
		res, err = is.Create(ctx, dstImg)
	} else {
		res, err = is.Update(ctx, dstImg)
	}
	return &res, err
}

// IsNydusBootstrap returns true when the specified descriptor is nydus bootstrap layer.
func IsNydusBootstrap(desc ocispec.Descriptor) bool {
	if desc.Annotations == nil {
		return false
	}

	_, hasAnno := desc.Annotations[LayerAnnotationNydusBootstrap]
	return hasAnno
}
