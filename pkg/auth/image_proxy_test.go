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
	runtime_alpha "github.com/containerd/containerd/third_party/k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type MockImageService struct {
	runtime_alpha.UnimplementedImageServiceServer
}

func (*MockImageService) PullImage(ctx context.Context, req *runtime_alpha.PullImageRequest) (*runtime_alpha.PullImageResponse, error) {
	return &runtime_alpha.PullImageResponse{}, nil
}

func TestFromImagePull(t *testing.T) {
	var err error
	assert := assert.New(t)

	ctx := context.TODO()
	d := t.TempDir()
	defer os.RemoveAll(d)

	tagImage := "docker.io/library/busybox:latest"
	// should return nil if no proxy
	kc, err := FromCRI("docker.io", tagImage)
	assert.Nil(kc)
	assert.NoError(err)

	mockRPC := grpc.NewServer()
	mockSocket := filepath.Join(d, "mock.sock")
	lm, err := net.Listen("unix", mockSocket)
	assert.NoError(err)

	// The server of CRI image service proxy.
	proxyRPC := grpc.NewServer()
	proxySocket := filepath.Join(d, "proxy.sock")
	lp, err := net.Listen("unix", proxySocket)
	assert.NoError(err)

	// Mocking the end CRI request consumer.
	server := &MockImageService{}
	runtime_alpha.RegisterImageServiceServer(mockRPC, server)
	go mockRPC.Serve(lm)
	defer lm.Close()

	AddImageProxy(ctx, proxyRPC, mockSocket)
	go proxyRPC.Serve(lp)
	defer lp.Close()

	kc, err = FromCRI("docker.io", tagImage)
	// should return empty kc before pulling
	assert.Nil(kc)
	assert.NoError(err)

	gopts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer.ContextDialer),
	}
	conn, err := grpc.Dial(dialer.DialAddress(proxySocket), gopts...)
	assert.NoError(err)
	criAlphaClient := runtime_alpha.NewImageServiceClient(conn)
	irAlpha := &runtime_alpha.PullImageRequest{
		Image: &runtime_alpha.ImageSpec{
			Image: tagImage,
		},
		Auth: &runtime_alpha.AuthConfig{
			Username: "test",
			Password: "passwd",
		},
	}
	criAlphaClient.PullImage(ctx, irAlpha)

	criClient := runtime.NewImageServiceClient(conn)

	kc, err = FromCRI("docker.io", tagImage)
	// get correct kc after pulling
	assert.Equal("test", kc.Username)
	assert.Equal("passwd", kc.Password)
	assert.NoError(err)

	kc, err = FromCRI("docker.io", "docker.io/library/busybox:another")
	// get empty kc with wrong tag
	assert.Nil(kc)
	assert.NoError(err)

	image2 := "ghcr.io/busybox:latest"

	ir := &runtime.PullImageRequest{
		Image: &runtime.ImageSpec{
			Image: image2,
		},
		Auth: &runtime.AuthConfig{
			Username: "test_1",
			Password: "passwd_1",
		},
	}
	criClient.PullImage(ctx, ir)

	kc, err = FromCRI("ghcr.io", image2)
	assert.Equal(kc.Username, "test_1")
	assert.Equal(kc.Password, "passwd_1")
	assert.NoError(err)

	// should work with digest
	digestImage := "docker.io/library/busybox@sha256:7cc4b5aefd1d0cadf8d97d4350462ba51c694ebca145b08d7d41b41acc8db5aa"
	irAlpha = &runtime_alpha.PullImageRequest{
		Image: &runtime_alpha.ImageSpec{
			Image: digestImage,
		},
		Auth: &runtime_alpha.AuthConfig{
			Username: "digest",
			Password: "dpwd",
		},
	}
	criAlphaClient.PullImage(ctx, irAlpha)

	kc, err = FromCRI("docker.io", digestImage)
	assert.Equal("digest", kc.Username)
	assert.Equal("dpwd", kc.Password)
	assert.NoError(err)

}
