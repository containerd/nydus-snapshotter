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
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

type S3Backend struct {
	// objectPrefix is the path prefix of the uploaded object.
	// For example, if the blobID which should be uploaded is "abc",
	// and the objectPrefix is "path/to/my-registry/", then the object key will be
	// "path/to/my-registry/abc".
	objectPrefix       string
	bucketName         string
	endpointWithScheme string
	region             string
	accessKeySecret    string
	accessKeyID        string
}

type S3Config struct {
	AccessKeyID     string `json:"access_key_id,omitempty"`
	AccessKeySecret string `json:"access_key_secret,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	Scheme          string `json:"scheme,omitempty"`
	BucketName      string `json:"bucket_name,omitempty"`
	Region          string `json:"region,omitempty"`
	ObjectPrefix    string `json:"object_prefix,omitempty"`
}

func newS3Backend(rawConfig []byte) (*S3Backend, error) {
	cfg := &S3Config{}
	if err := json.Unmarshal(rawConfig, cfg); err != nil {
		return nil, errors.Wrap(err, "parse S3 storage backend configuration")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "s3.amazonaws.com"
	}
	if cfg.Scheme == "" {
		cfg.Scheme = "https"
	}
	endpointWithScheme := fmt.Sprintf("%s://%s", cfg.Scheme, cfg.Endpoint)

	if cfg.BucketName == "" || cfg.Region == "" {
		return nil, fmt.Errorf("invalid S3 configuration: missing 'bucket_name' or 'region'")
	}

	return &S3Backend{
		objectPrefix:       cfg.ObjectPrefix,
		bucketName:         cfg.BucketName,
		region:             cfg.Region,
		endpointWithScheme: endpointWithScheme,
		accessKeySecret:    cfg.AccessKeySecret,
		accessKeyID:        cfg.AccessKeyID,
	}, nil
}

func (b *S3Backend) client() (*s3.Client, error) {
	s3AWSConfig, err := awscfg.LoadDefaultConfig(context.TODO())
	if err != nil {
		return nil, errors.Wrap(err, "load default AWS config")
	}

	client := s3.NewFromConfig(s3AWSConfig, func(o *s3.Options) {
		o.EndpointResolver = s3.EndpointResolverFromURL(b.endpointWithScheme)
		o.Region = b.region
		o.UsePathStyle = true
		if len(b.accessKeySecret) > 0 && len(b.accessKeyID) > 0 {
			o.Credentials = credentials.NewStaticCredentialsProvider(b.accessKeyID, b.accessKeySecret, "")
		}
		o.UsePathStyle = true
	})
	return client, nil
}

func (b *S3Backend) existObject(ctx context.Context, objectKey string) (bool, error) {
	client, err := b.client()
	if err != nil {
		return false, errors.Wrap(err, "failed to create s3 client")
	}
	_, err = client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &b.bucketName,
		Key:    &objectKey,
	})
	if err != nil {
		var responseError *awshttp.ResponseError
		if errors.As(err, &responseError) && responseError.ResponseError.HTTPStatusCode() == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (b *S3Backend) Push(ctx context.Context, ra content.ReaderAt, blobDigest digest.Digest) error {
	blobID := blobDigest.Hex()
	blobObjectKey := b.objectPrefix + blobID

	if exist, err := b.existObject(ctx, blobObjectKey); err != nil {
		return errors.Wrap(err, "check object existence")
	} else if exist {
		return nil
	}

	reader := content.NewReader(ra)

	client, err := b.client()
	if err != nil {
		return errors.Wrap(err, "failed to create s3 client")
	}

	uploader := manager.NewUploader(client, func(u *manager.Uploader) {
		u.PartSize = MultipartsUploadThreshold
	})
	if _, err := uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:            aws.String(b.bucketName),
		Key:               aws.String(blobObjectKey),
		Body:              reader,
		ChecksumAlgorithm: types.ChecksumAlgorithmCrc32,
	}); err != nil {
		return errors.Wrap(err, "push blob to s3 backend")
	}

	return nil
}

func (b *S3Backend) Check(blobDigest digest.Digest) (string, error) {
	blobID := blobDigest.Hex()
	objectKey := b.objectPrefix + blobDigest.Hex()
	if exist, err := b.existObject(context.Background(), objectKey); err != nil {
		return "", err
	} else if exist {
		return blobID, nil
	}
	return "", errdefs.ErrNotFound

}

func (b *S3Backend) Type() string {
	return BackendTypeS3
}
