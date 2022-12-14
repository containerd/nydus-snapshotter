/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/pkg/dialer"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

type MockImageService struct {
	runtime.UnimplementedImageServiceServer
}

func (*MockImageService) PullImage(ctx context.Context, req *runtime.PullImageRequest) (*runtime.PullImageResponse, error) {
	return &runtime.PullImageResponse{}, nil
}

func TestFromImagePull(t *testing.T) {
	var err error
	assert := assert.New(t)

	ctx := context.TODO()
	d := t.TempDir()
	defer os.RemoveAll(d)

	tagImage := "docker.io/library/busybox:latest"
	// should return nil if no proxy
	kc := FromCRI("docker.io", tagImage)
	assert.Nil(kc)

	mockRPC := grpc.NewServer()
	mockSocket := filepath.Join(d, "mock.sock")
	lm, err := net.Listen("unix", mockSocket)
	assert.NoError(err)

	proxyRPC := grpc.NewServer()
	proxySocket := filepath.Join(d, "proxy.sock")
	lp, err := net.Listen("unix", proxySocket)
	assert.NoError(err)

	server := &MockImageService{}
	runtime.RegisterImageServiceServer(mockRPC, server)
	go mockRPC.Serve(lm)
	defer lm.Close()

	AddImageProxy(ctx, proxyRPC, mockSocket)
	go proxyRPC.Serve(lp)
	defer lp.Close()

	kc = FromCRI("docker.io", tagImage)
	// should return empty kc before pulling
	assert.Empty(kc.Password)
	assert.Empty(kc.Username)

	gopts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer.ContextDialer),
	}
	conn, err := grpc.Dial(dialer.DialAddress(proxySocket), gopts...)
	assert.NoError(err)
	criClient := runtime.NewImageServiceClient(conn)
	ir := &runtime.PullImageRequest{
		Image: &runtime.ImageSpec{
			Image: tagImage,
		},
		Auth: &runtime.AuthConfig{
			Username: "test",
			Password: "passwd",
		},
	}
	criClient.PullImage(ctx, ir)

	kc = FromCRI("docker.io", tagImage)
	// get correct kc after pulling
	assert.Equal("test", kc.Username)
	assert.Equal("passwd", kc.Password)

	kc = FromCRI("docker.io", "docker.io/library/busybox:another")
	// get empty kc with wrong tag
	assert.Empty(kc.Password)
	assert.Empty(kc.Username)

	// should work with digest
	digestImage := "docker.io/library/busybox@sha256:7cc4b5aefd1d0cadf8d97d4350462ba51c694ebca145b08d7d41b41acc8db5aa"
	ir = &runtime.PullImageRequest{
		Image: &runtime.ImageSpec{
			Image: digestImage,
		},
		Auth: &runtime.AuthConfig{
			Username: "digest",
			Password: "dpwd",
		},
	}
	criClient.PullImage(ctx, ir)

	kc = FromCRI("docker.io", digestImage)
	assert.Equal("digest", kc.Username)
	assert.Equal("dpwd", kc.Password)
}
