/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/containerd/defaults"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/dialer"
	"github.com/containerd/containerd/reference"
	distribution "github.com/containerd/containerd/reference/docker"
	"github.com/containerd/stargz-snapshotter/service/keychain/crialpha"
	"github.com/containerd/stargz-snapshotter/service/resolver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"

	runtime_alpha "github.com/containerd/containerd/third_party/k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

const DefaultImageServiceAddress = "/run/containerd/containerd.sock"

var Cred resolver.Credential

// from stargz-snapshotter/cmd/containerd-stargz-grpc/main.go#main
func AddImageProxy(ctx context.Context, rpc *grpc.Server, imageServiceAddress string) {
	criAddr := DefaultImageServiceAddress
	if imageServiceAddress != "" {
		criAddr = imageServiceAddress
	}
	connectCRI := func() (runtime_alpha.ImageServiceClient, error) {
		// TODO: make gRPC options configurable from config.toml
		backoffConfig := backoff.DefaultConfig
		backoffConfig.MaxDelay = 3 * time.Second
		connParams := grpc.ConnectParams{
			Backoff: backoffConfig,
		}
		gopts := []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithConnectParams(connParams),
			grpc.WithContextDialer(dialer.ContextDialer),
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(defaults.DefaultMaxRecvMsgSize)),
			grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(defaults.DefaultMaxSendMsgSize)),
		}
		conn, err := grpc.Dial(dialer.DialAddress(criAddr), gopts...)
		if err != nil {
			return nil, err
		}
		return runtime_alpha.NewImageServiceClient(conn), nil
	}

	var criAlphaServer runtime_alpha.ImageServiceServer
	Cred, criAlphaServer = crialpha.NewCRIAlphaKeychain(ctx, connectCRI)
	runtime_alpha.RegisterImageServiceServer(rpc, criAlphaServer)
	log.G(ctx).WithField("target-image-service", criAddr).Info("setup image proxy keychain")
}

func FromCRI(host, ref string) *PassKeyChain {
	if Cred == nil {
		return nil
	}

	refSpec, err := parseReference(ref)
	if err != nil {
		log.L.WithError(err).Error("parse ref failed")
		return nil
	}

	u, p, err := Cred(host, refSpec)
	if err != nil {
		log.L.WithError(err).Error("get credential failed")
		return nil
	}

	return &PassKeyChain{
		Username: u,
		Password: p,
	}
}

// from stargz-snapshotter/service/keychain/cri/cri.go
func parseReference(ref string) (reference.Spec, error) {
	namedRef, err := distribution.ParseDockerRef(ref)
	if err != nil {
		return reference.Spec{}, fmt.Errorf("failed to parse image reference %q: %w", ref, err)
	}
	return reference.Parse(namedRef.String())
}
