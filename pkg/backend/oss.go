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
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

const (
	splitPartsCount = 4
)

type OSSBackend struct {
	// OSS storage does not support directory. Therefore add a prefix to each object
	// to make it a path-like object.
	objectPrefix string
	bucket       *oss.Bucket
}

func newOSSBackend(rawConfig []byte) (*OSSBackend, error) {
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
	}, nil
}

// Ported from https://github.com/aliyun/aliyun-oss-go-sdk/blob/c82fb81e272d84f716d3f13c36fe0542a49adfeb/oss/utils.go#L207.
func splitBlobByPartNum(blobSize int64, chunkNum int) ([]oss.FileChunk, error) {
	if chunkNum <= 0 || chunkNum > 10000 {
		return nil, errors.New("chunkNum invalid")
	}

	if int64(chunkNum) > blobSize {
		return nil, errors.New("oss: chunkNum invalid")
	}

	var chunks []oss.FileChunk
	var chunk = oss.FileChunk{}
	var chunkN = (int64)(chunkNum)
	for i := int64(0); i < chunkN; i++ {
		chunk.Number = int(i + 1)
		chunk.Offset = i * (blobSize / chunkN)
		if i == chunkN-1 {
			chunk.Size = blobSize/chunkN + blobSize%chunkN
		} else {
			chunk.Size = blobSize / chunkN
		}
		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

// Upload nydus blob to oss storage backend.
func (b *OSSBackend) push(ra content.ReaderAt, blobDigest digest.Digest) error {
	blobID := blobDigest.Hex()
	blobObjectKey := b.objectPrefix + blobID

	if exist, err := b.bucket.IsObjectExist(blobObjectKey); err != nil {
		return errors.Wrap(err, "check object existence")
	} else if exist {
		return nil
	}

	blobSize := ra.Size()
	var needMultiparts = false
	// Blob size bigger than 100MB, apply multiparts upload.
	if blobSize >= MultipartsUploadThreshold {
		needMultiparts = true
	}

	if needMultiparts {
		chunks, err := splitBlobByPartNum(blobSize, splitPartsCount)
		if err != nil {
			return errors.Wrap(err, "split blob by part num")
		}

		imur, err := b.bucket.InitiateMultipartUpload(blobObjectKey)
		if err != nil {
			return errors.Wrap(err, "initiate multipart upload")
		}

		// It always splits the blob into splitPartsCount=4 parts
		partsChan := make(chan oss.UploadPart, splitPartsCount)

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
			if err := b.bucket.AbortMultipartUpload(imur); err != nil {
				close(partsChan)
				return errors.Wrap(err, "aborting upload")
			}
			close(partsChan)
			return errors.Wrap(err, "upload parts")
		}

		close(partsChan)

		var parts []oss.UploadPart
		for p := range partsChan {
			parts = append(parts, p)
		}

		if _, err = b.bucket.CompleteMultipartUpload(imur, parts); err != nil {
			return errors.Wrap(err, "complete multipart upload")
		}
	} else {
		reader := content.NewReader(ra)
		if err := b.bucket.PutObject(blobObjectKey, reader); err != nil {
			return errors.Wrap(err, "put blob object")
		}
	}

	return nil
}

func (b *OSSBackend) Push(ctx context.Context, ra content.ReaderAt, blobDigest digest.Digest) error {
	backoff := time.Second
	for {
		err := b.push(ra, blobDigest)
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
