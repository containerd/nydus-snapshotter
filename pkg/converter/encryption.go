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

package converter

import (
	"context"
	"fmt"
	"io"
	"math/rand"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/imgcrypt/images/encryption/parsehelpers"

	"github.com/containers/ocicrypt"
	encconfig "github.com/containers/ocicrypt/config"
	encocispec "github.com/containers/ocicrypt/spec"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type cryptoOp int

const (
	cryptoOpEncrypt    cryptoOp = iota
	cryptoOpDecrypt             = iota
	cryptoOpUnwrapOnly          = iota
)

// LayerFilter allows to select Layers by certain criteria
type LayerFilter func(desc ocispec.Descriptor) bool

// IsEncryptedDiff returns true if mediaType is a known encrypted media type.
func IsEncryptedDiff(ctx context.Context, mediaType string) bool {
	switch mediaType {
	case encocispec.MediaTypeLayerZstdEnc, encocispec.MediaTypeLayerGzipEnc, encocispec.MediaTypeLayerEnc:
		return true
	}
	return false
}

// HasEncryptedLayer returns true if any LayerInfo indicates that the layer is encrypted
func HasEncryptedLayer(ctx context.Context, layerInfos []ocispec.Descriptor) bool {
	for i := 0; i < len(layerInfos); i++ {
		if IsEncryptedDiff(ctx, layerInfos[i].MediaType) {
			return true
		}
	}
	return false
}

// encryptLayer encrypts the layer using the CryptoConfig and creates a new OCI Descriptor.
// A call to this function may also only manipulate the wrapped keys list.
// The caller is expected to store the returned encrypted data and OCI Descriptor
func encryptLayer(cc *encconfig.CryptoConfig, dataReader content.ReaderAt, desc ocispec.Descriptor) (ocispec.Descriptor, io.Reader, ocicrypt.EncryptLayerFinalizer, error) {
	var (
		size int64
		d    digest.Digest
		err  error
	)

	encLayerReader, encLayerFinalizer, err := ocicrypt.EncryptLayer(cc.EncryptConfig, ocicrypt.ReaderFromReaderAt(dataReader), desc)
	if err != nil {
		return ocispec.Descriptor{}, nil, nil, err
	}

	// were data touched ?
	if encLayerReader != nil {
		size = 0
		d = ""
	} else {
		size = desc.Size
		d = desc.Digest
	}

	newDesc := ocispec.Descriptor{
		Digest:   d,
		Size:     size,
		Platform: desc.Platform,
	}

	switch desc.MediaType {
	case images.MediaTypeDockerSchema2LayerGzip:
		newDesc.MediaType = encocispec.MediaTypeLayerGzipEnc
	case images.MediaTypeDockerSchema2Layer:
		newDesc.MediaType = encocispec.MediaTypeLayerEnc
	case encocispec.MediaTypeLayerGzipEnc:
		newDesc.MediaType = encocispec.MediaTypeLayerGzipEnc
	case encocispec.MediaTypeLayerZstdEnc:
		newDesc.MediaType = encocispec.MediaTypeLayerZstdEnc
	case encocispec.MediaTypeLayerEnc:
		newDesc.MediaType = encocispec.MediaTypeLayerEnc

	// TODO: Mediatypes to be added in ocispec
	case ocispec.MediaTypeImageLayerGzip:
		newDesc.MediaType = encocispec.MediaTypeLayerGzipEnc
	case ocispec.MediaTypeImageLayerZstd:
		newDesc.MediaType = encocispec.MediaTypeLayerZstdEnc
	case ocispec.MediaTypeImageLayer:
		newDesc.MediaType = encocispec.MediaTypeLayerEnc

	default:
		return ocispec.Descriptor{}, nil, nil, fmt.Errorf("unsupporter layer MediaType: %s", desc.MediaType)
	}

	return newDesc, encLayerReader, encLayerFinalizer, nil
}

// DecryptLayer decrypts the layer using the DecryptConfig and creates a new OCI Descriptor.
// The caller is expected to store the returned plain data and OCI Descriptor
func DecryptLayer(dc *encconfig.DecryptConfig, dataReader io.Reader, desc ocispec.Descriptor, unwrapOnly bool) (ocispec.Descriptor, io.Reader, digest.Digest, error) {
	resultReader, layerDigest, err := ocicrypt.DecryptLayer(dc, dataReader, desc, unwrapOnly)
	if err != nil || unwrapOnly {
		return ocispec.Descriptor{}, nil, "", err
	}

	newDesc := ocispec.Descriptor{
		Size:     0,
		Platform: desc.Platform,
	}

	switch desc.MediaType {
	case encocispec.MediaTypeLayerGzipEnc:
		newDesc.MediaType = images.MediaTypeDockerSchema2LayerGzip
	case encocispec.MediaTypeLayerZstdEnc:
		newDesc.MediaType = ocispec.MediaTypeImageLayerZstd
	case encocispec.MediaTypeLayerEnc:
		newDesc.MediaType = images.MediaTypeDockerSchema2Layer
	default:
		return ocispec.Descriptor{}, nil, "", fmt.Errorf("unsupporter layer MediaType: %s", desc.MediaType)
	}
	return newDesc, resultReader, layerDigest, nil
}

// decryptLayer decrypts the layer using the CryptoConfig and creates a new OCI Descriptor.
// The caller is expected to store the returned plain data and OCI Descriptor
func decryptLayer(cc *encconfig.CryptoConfig, dataReader content.ReaderAt, desc ocispec.Descriptor, unwrapOnly bool) (ocispec.Descriptor, io.Reader, error) {
	resultReader, d, err := ocicrypt.DecryptLayer(cc.DecryptConfig, ocicrypt.ReaderFromReaderAt(dataReader), desc, unwrapOnly)
	if err != nil || unwrapOnly {
		return ocispec.Descriptor{}, nil, err
	}

	newDesc := ocispec.Descriptor{
		Digest:   d,
		Size:     0,
		Platform: desc.Platform,
	}

	switch desc.MediaType {
	case encocispec.MediaTypeLayerGzipEnc:
		newDesc.MediaType = images.MediaTypeDockerSchema2LayerGzip
	case encocispec.MediaTypeLayerZstdEnc:
		newDesc.MediaType = ocispec.MediaTypeImageLayerZstd
	case encocispec.MediaTypeLayerEnc:
		newDesc.MediaType = images.MediaTypeDockerSchema2Layer
	default:
		return ocispec.Descriptor{}, nil, fmt.Errorf("unsupporter layer MediaType: %s", desc.MediaType)
	}
	return newDesc, resultReader, nil
}

// cryptLayer handles the changes due to encryption or decryption of a layer
func cryptLayer(ctx context.Context, cs content.Store, desc ocispec.Descriptor, opt MergeOption, cryptoOp cryptoOp) (ocispec.Descriptor, error) {
	var (
		resultReader      io.Reader
		newDesc           ocispec.Descriptor
		encLayerFinalizer ocicrypt.EncryptLayerFinalizer
		arg               parsehelpers.EncArgs
	)

	dataReader, err := cs.ReaderAt(ctx, desc)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer dataReader.Close()

	arg.Recipient = []string{opt.EncryptRecipient}
	cc, err := parsehelpers.CreateCryptoConfig(arg, []ocispec.Descriptor{desc})
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	if cryptoOp == cryptoOpEncrypt {
		newDesc, resultReader, encLayerFinalizer, err = encryptLayer(&cc, dataReader, desc)
	} else {
		newDesc, resultReader, err = decryptLayer(&cc, dataReader, desc, cryptoOp == cryptoOpUnwrapOnly)
	}
	if err != nil || cryptoOp == cryptoOpUnwrapOnly {
		return ocispec.Descriptor{}, err
	}

	newDesc.Annotations = ocicrypt.FilterOutAnnotations(desc.Annotations)

	// some operations, such as changing recipients, may not touch the layer at all
	if resultReader != nil {
		var ref string
		// If we have the digest, write blob with checks
		haveDigest := newDesc.Digest.String() != ""
		if haveDigest {
			ref = fmt.Sprintf("bootstrap-encrypted-%s", newDesc.Digest.String())
		} else {
			ref = fmt.Sprintf("bootstrap-encrypted-%d-%d", rand.Int(), rand.Int())
		}

		if haveDigest {
			if err := content.WriteBlob(ctx, cs, ref, resultReader, newDesc); err != nil {
				return ocispec.Descriptor{}, fmt.Errorf("failed to write config: %w", err)
			}
		} else {
			newDesc.Digest, newDesc.Size, err = ingestReader(ctx, cs, ref, resultReader)
			if err != nil {
				return ocispec.Descriptor{}, err
			}
		}
	}

	// After performing encryption, call finalizer to get annotations
	if encLayerFinalizer != nil {
		annotations, err := encLayerFinalizer()
		if err != nil {
			return ocispec.Descriptor{}, fmt.Errorf("error getting annotations from encLayer finalizer: %w", err)
		}
		for k, v := range annotations {
			newDesc.Annotations[k] = v
		}
	}
	return newDesc, err
}

func ingestReader(ctx context.Context, cs content.Ingester, ref string, r io.Reader) (digest.Digest, int64, error) {
	cw, err := content.OpenWriter(ctx, cs, content.WithRef(ref))
	if err != nil {
		return "", 0, fmt.Errorf("failed to open writer: %w", err)
	}
	defer cw.Close()

	if _, err := content.CopyReader(cw, r); err != nil {
		return "", 0, fmt.Errorf("copy failed: %w", err)
	}

	st, err := cw.Status()
	if err != nil {
		return "", 0, fmt.Errorf("failed to get state: %w", err)
	}

	if err := cw.Commit(ctx, st.Offset, ""); err != nil {
		if !errdefs.IsAlreadyExists(err) {
			return "", 0, fmt.Errorf("failed commit on ref %q: %w", ref, err)
		}
	}

	return cw.Digest(), st.Offset, nil
}
