/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/pkg/dialer"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type MockImageService struct {
	runtime.UnimplementedImageServiceServer
}

func (*MockImageService) PullImage(_ context.Context, _ *runtime.PullImageRequest) (*runtime.PullImageResponse, error) {
	return &runtime.PullImageResponse{}, nil
}

func TestFromImagePull(t *testing.T) {
	var err error
	assertions := assert.New(t)

	ctx := context.TODO()
	d := t.TempDir()

	tagImage := "docker.io/library/busybox:latest"

	// should return nil if no proxy
	kc, err := FromCRI("docker.io", tagImage)
	assertions.Nil(kc)
	assertions.NoError(err)

	// Mocking the end CRI request consumer.
	mockRPC := grpc.NewServer()
	mockSocket := filepath.Join(d, "mock.sock")
	listenMock, err := net.Listen("unix", mockSocket)
	assertions.NoError(err)

	// Mocking the end CRI request consumer.
	server := &MockImageService{}
	runtime.RegisterImageServiceServer(mockRPC, server)

	go func() {
		err := mockRPC.Serve(listenMock)
		assertions.NoError(err)
	}()
	defer mockRPC.Stop()

	// The server of CRI image service proxy.
	proxyRPC := grpc.NewServer()
	proxySocket := filepath.Join(d, "proxy.sock")
	listenProxy, err := net.Listen("unix", proxySocket)
	assertions.NoError(err)
	AddImageProxy(ctx, proxyRPC, mockSocket)
	go func() {
		err := proxyRPC.Serve(listenProxy)
		assertions.NoError(err)
	}()
	defer proxyRPC.Stop()

	// should return empty kc before pulling
	kc, err = FromCRI("docker.io", tagImage)
	assertions.Nil(kc)
	assertions.NoError(err)

	gopts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer.ContextDialer),
	}

	conn, err := grpc.NewClient(dialer.DialAddress(proxySocket), gopts...)
	assertions.NoError(err)
	criClient := runtime.NewImageServiceClient(conn)

	_, err = criClient.PullImage(ctx, &runtime.PullImageRequest{
		Image: &runtime.ImageSpec{
			Image: tagImage,
		},
		Auth: &runtime.AuthConfig{
			Username: "test",
			Password: "passwd",
		},
	})
	assertions.NoError(err)

	// get correct kc after pulling
	kc, err = FromCRI("docker.io", tagImage)
	assertions.Equal("test", kc.Username)
	assertions.Equal("passwd", kc.Password)
	assertions.NoError(err)

	// get empty kc with wrong tag
	kc, err = FromCRI("docker.io", "docker.io/library/busybox:another")
	assertions.Nil(kc)
	assertions.NoError(err)

	image2 := "ghcr.io/busybox:latest"

	_, err = criClient.PullImage(ctx, &runtime.PullImageRequest{
		Image: &runtime.ImageSpec{
			Image: image2,
		},
		Auth: &runtime.AuthConfig{
			Username: "test_1",
			Password: "passwd_1",
		},
	})
	assertions.NoError(err)

	// get correct kc after pulling
	kc, err = FromCRI("ghcr.io", image2)
	assertions.NoError(err)
	assertions.Equal(kc.Username, "test_1")
	assertions.Equal(kc.Password, "passwd_1")

	// should work with digest
	digestImage := "docker.io/library/busybox@sha256:7cc4b5aefd1d0cadf8d97d4350462ba51c694ebca145b08d7d41b41acc8db5aa"
	_, err = criClient.PullImage(ctx, &runtime.PullImageRequest{
		Image: &runtime.ImageSpec{
			Image: digestImage,
		},
		Auth: &runtime.AuthConfig{
			Username: "digest",
			Password: "dpwd",
		},
	})
	assertions.NoError(err)

	// get correct kc after pulling
	kc, err = FromCRI("docker.io", digestImage)
	assertions.NoError(err)
	assertions.Equal("digest", kc.Username)
	assertions.Equal("dpwd", kc.Password)
}
