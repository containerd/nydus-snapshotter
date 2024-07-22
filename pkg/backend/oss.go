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
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

type OSSBackend struct {
	// OSS storage does not support directory. Therefore add a prefix to each object
	// to make it a path-like object.
	objectPrefix string
	bucket       *oss.Bucket
	forcePush    bool
}

func newOSSBackend(rawConfig []byte, forcePush bool) (*OSSBackend, error) {
	var configMap map[string]string
	if err := json.Unmarshal(rawConfig, &configMap); err != nil {
		return nil, errors.Wrap(err, "Parse OSS storage backend configuration")
	}

	endpoint, ok1 := configMap["endpoint"]
	bucketName, ok2 := configMap["bucket_name"]

	// Below fields are not mandatory.
	accessKeyID := configMap["access_key_id"]
	accessKeySecret := configMap["access_key_secret"]
	objectPrefix := configMap["object_prefix"]

	if !ok1 || !ok2 {
		return nil, fmt.Errorf("no endpoint or bucket is specified")
	}

	client, err := oss.New(endpoint, accessKeyID, accessKeySecret)
	if err != nil {
		return nil, errors.Wrap(err, "Create client")
	}

	bucket, err := client.Bucket(bucketName)
	if err != nil {
		return nil, errors.Wrap(err, "Create bucket")
	}

	return &OSSBackend{
		objectPrefix: objectPrefix,
		bucket:       bucket,
		forcePush:    forcePush,
	}, nil
}

// Ported from https://github.com/aliyun/aliyun-oss-go-sdk/blob/v2.2.6/oss/utils.go#L259
func splitFileByPartSize(blobSize, chunkSize int64) ([]oss.FileChunk, error) {
	if chunkSize <= 0 {
		return nil, errors.New("invalid chunk size")
	}

	var chunkN = blobSize / chunkSize
	if chunkN >= 10000 {
		return nil, errors.New("too many parts, please increase chunk size")
	}

	var chunks []oss.FileChunk
	var chunk = oss.FileChunk{}
	for i := int64(0); i < chunkN; i++ {
		chunk.Number = int(i + 1)
		chunk.Offset = i * chunkSize
		chunk.Size = chunkSize
		chunks = append(chunks, chunk)
	}

	if blobSize%chunkSize > 0 {
		chunk.Number = len(chunks) + 1
		chunk.Offset = int64(len(chunks)) * chunkSize
		chunk.Size = blobSize % chunkSize
		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

// Upload nydus blob to oss storage backend.
func (b *OSSBackend) push(ctx context.Context, cs content.Store, desc ocispec.Descriptor) error {
	blobID := desc.Digest.Hex()
	blobObjectKey := b.objectPrefix + blobID

	ra, err := cs.ReaderAt(ctx, desc)
	if err != nil {
		return errors.Wrapf(err, "get reader for compression blob %q", desc.Digest)
	}
	defer ra.Close()

	if exist, err := b.bucket.IsObjectExist(blobObjectKey); err != nil {
		return errors.Wrap(err, "check object existence")
	} else if exist && !b.forcePush {
		return nil
	}

	chunks, err := splitFileByPartSize(ra.Size(), MultipartChunkSize)
	if err != nil {
		return errors.Wrap(err, "split blob by part num")
	}

	imur, err := b.bucket.InitiateMultipartUpload(blobObjectKey)
	if err != nil {
		return errors.Wrap(err, "initiate multipart upload")
	}
	partsChan := make(chan oss.UploadPart, len(chunks))

	g := new(errgroup.Group)
	for _, chunk := range chunks {
		ck := chunk
		g.Go(func() error {
			p, err := b.bucket.UploadPart(imur, io.NewSectionReader(ra, ck.Offset, ck.Size), ck.Size, ck.Number)
			if err != nil {
				return errors.Wrap(err, "upload part")
			}
			partsChan <- p
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		_ = b.bucket.AbortMultipartUpload(imur)
		close(partsChan)
		return errors.Wrap(err, "upload parts")
	}
	close(partsChan)

	parts := make([]oss.UploadPart, 0, 16)
	for p := range partsChan {
		parts = append(parts, p)
	}

	_, err = b.bucket.CompleteMultipartUpload(imur, parts)
	if err != nil {
		return errors.Wrap(err, "complete multipart upload")
	}

	return nil
}

func (b *OSSBackend) Push(ctx context.Context, cs content.Store, desc ocispec.Descriptor) error {
	backoff := time.Second
	for {
		err := b.push(ctx, cs, desc)
		if err != nil {
			select {
			case <-ctx.Done():
				return err
			default:
			}
		} else {
			return nil
		}
		if backoff >= 8*time.Second {
			return err
		}
		time.Sleep(backoff)
		backoff *= 2
	}
}

func (b *OSSBackend) Check(blobDigest digest.Digest) (string, error) {
	blobID := blobDigest.Hex()
	blobObjectKey := b.objectPrefix + blobID
	if exist, err := b.bucket.IsObjectExist(blobObjectKey); err != nil {
		return "", err
	} else if exist {
		return blobID, nil
	}
	return "", errdefs.ErrNotFound
}

func (b *OSSBackend) Type() string {
	return BackendTypeOSS
}
