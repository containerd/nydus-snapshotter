/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package encryption

import (
	"context"
	"fmt"
	"io"
	"math/rand"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"

	"github.com/containers/ocicrypt"
	encconfig "github.com/containers/ocicrypt/config"
	enchelpers "github.com/containers/ocicrypt/helpers"
	encocispec "github.com/containers/ocicrypt/spec"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Copied from containerd/imgcrypt project, copyright The imgcrypt Authors.
// https://github.com/containerd/imgcrypt/blob/e7500301cabcc9f3cab3daee3f541079b509e95f/images/encryption/encryption.go#LL82C5-L82C5
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

// Copied from containerd/imgcrypt project, copyright The imgcrypt Authors.
// https://github.com/containerd/imgcrypt/blob/e7500301cabcc9f3cab3daee3f541079b509e95f/images/encryption/encryption.go#LL164C11-L164C11
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

// Copied from containerd/imgcrypt project, copyright The imgcrypt Authors.
// https://github.com/containerd/imgcrypt/blob/e7500301cabcc9f3cab3daee3f541079b509e95f/images/encryption/encryption.go#LL250C5-L250C5
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

// Encrypt Nydus bootstrap layer
func EncryptNydusBootstrap(ctx context.Context, cs content.Store, desc ocispec.Descriptor, encryptRecipients []string) (ocispec.Descriptor, error) {
	var (
		resultReader      io.Reader
		newDesc           ocispec.Descriptor
		encLayerFinalizer ocicrypt.EncryptLayerFinalizer
	)

	dataReader, err := cs.ReaderAt(ctx, desc)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer dataReader.Close()

	cc, err := enchelpers.CreateCryptoConfig(encryptRecipients, []string{})
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("create encrypt config failed: %w", err)
	}
	newDesc, resultReader, encLayerFinalizer, err = encryptLayer(&cc, dataReader, desc)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("failed to encrypt bootstrap layer: %w", err)
	}
	newDesc.Annotations = ocicrypt.FilterOutAnnotations(desc.Annotations)

	// some operations, such as changing recipients, may not touch the layer at all
	if resultReader != nil {
		var ref string
		// If we have the digest, write blob with checks
		haveDigest := newDesc.Digest.String() != ""
		if haveDigest {
			ref = fmt.Sprintf("encrypted-bootstrap-%s", newDesc.Digest.String())
		} else {
			ref = fmt.Sprintf("encrypted-bootstrap-%d-%d", rand.Int(), rand.Int())
		}

		if haveDigest {
			// Write blob if digest is known beforehand
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

// Decrypt the Nydus boostrap layer.
// If unwrapOnly is set we will only try to decrypt the layer encryption key and return,
// the layer itself won't be decrypted actually.
func DeryptNydusBootstrap(ctx context.Context, cs content.Store, desc ocispec.Descriptor, decryptKeys []string, unwrapOnly bool) (ocispec.Descriptor, error) {
	var (
		resultReader io.Reader
		newDesc      ocispec.Descriptor
	)

	dataReader, err := cs.ReaderAt(ctx, desc)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer dataReader.Close()

	cc, err := enchelpers.CreateCryptoConfig([]string{}, decryptKeys)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("create decrypt config failed: %w", err)
	}
	newDesc, resultReader, err = decryptLayer(&cc, dataReader, desc, unwrapOnly)
	if err != nil || unwrapOnly {
		return ocispec.Descriptor{}, fmt.Errorf("failed to decrypt bootstrap layer: %w", err)
	}

	newDesc.Annotations = ocicrypt.FilterOutAnnotations(desc.Annotations)

	// some operations, such as changing recipients, may not touch the layer at all
	if resultReader != nil {
		var ref string
		// If we have the digest, write blob with checks
		haveDigest := newDesc.Digest.String() != ""
		if haveDigest {
			ref = fmt.Sprintf("decrypted-bootstrap-%s", newDesc.Digest.String())
		} else {
			ref = fmt.Sprintf("decrypted-bootstrap-%d-%d", rand.Int(), rand.Int())
		}

		if haveDigest {
			// Write blob if digest is known beforehand
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
	return newDesc, err
}
