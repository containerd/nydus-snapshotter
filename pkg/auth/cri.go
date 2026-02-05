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

	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/pkg/dialer"
	"github.com/containerd/log"
	"github.com/containerd/stargz-snapshotter/service/keychain/cri"
	"github.com/containerd/stargz-snapshotter/service/resolver"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const DefaultImageServiceAddress = "/run/containerd/containerd.sock"

// TODO: embed in CRIProvider
// Should be concurrency safe
var credentials []resolver.Credential = make([]resolver.Credential, 0, 8)

// CRIProvider retrieves credentials from CRI image pull requests.
type CRIProvider struct{}

// NewCRIProvider creates a new CRI-based auth provider.
func NewCRIProvider() *CRIProvider {
	return &CRIProvider{}
}

func (p *CRIProvider) GetCredentials(req *AuthRequest) (*PassKeyChain, error) {
	if len(credentials) == 0 {
		return nil, errors.New("no Credentials parsers")
	}

	if req == nil || req.Ref == "" {
		return nil, errors.New("ref not found in request")
	}

	refSpec, host, err := parseReference(req.Ref)
	if err != nil {
		return nil, errors.Wrapf(err, "parse reference %s", req.Ref)
	}

	var u, s string
	var keychain *PassKeyChain

	for _, cred := range credentials {
		if username, secret, err := cred(host, refSpec); err != nil {
			return nil, err
		} else if username != "" || secret != "" {
			u = username
			s = secret

			keychain = &PassKeyChain{
				Username: u,
				Password: s,
			}

			return keychain, nil
		}
	}

	return nil, fmt.Errorf("no credentials found for host: %s", host)
}

// newCRIConn creates a gRPC connection to the CRI service.
// This function is borrowed from stargz
func newCRIConn(criAddr string) (*grpc.ClientConn, error) {
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
	return grpc.NewClient(dialer.DialAddress(criAddr), gopts...)
}

// AddImageProxy sets up a CRI image proxy that intercepts credentials.
// This should be called once at startup to enable CRI credential capture.
// from stargz-snapshotter/cmd/containerd-stargz-grpc/main.go#main
func AddImageProxy(ctx context.Context, rpc *grpc.Server, imageServiceAddress string) {
	criAddr := DefaultImageServiceAddress
	if imageServiceAddress != "" {
		criAddr = imageServiceAddress
	}

	criCred, criServer := cri.NewCRIKeychain(ctx, func() (runtime.ImageServiceClient, error) {
		conn, err := newCRIConn(criAddr)
		if err != nil {
			return nil, err
		}

		return runtime.NewImageServiceClient(conn), nil
	})

	runtime.RegisterImageServiceServer(rpc, criServer)

	credentials = append(credentials, criCred)

	log.G(ctx).WithField("target-image-service", criAddr).Info("setup image proxy keychain")
}
