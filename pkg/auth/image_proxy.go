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
	"github.com/containerd/containerd/v2/pkg/reference"
	"github.com/containerd/log"
	"github.com/containerd/stargz-snapshotter/service/keychain/cri"
	"github.com/containerd/stargz-snapshotter/service/resolver"
	distribution "github.com/distribution/reference"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const DefaultImageServiceAddress = "/run/containerd/containerd.sock"

// Should be concurrency safe
var Credentials []resolver.Credential = make([]resolver.Credential, 0, 8)

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

	Credentials = append(Credentials, criCred)

	log.G(ctx).WithField("target-image-service", criAddr).Info("setup image proxy keychain")
}

func FromCRI(host, ref string) (*PassKeyChain, error) {
	if Credentials == nil {
		return nil, errors.New("No Credentials parsers")
	}

	refSpec, err := parseReference(ref)
	if err != nil {
		log.L.WithError(err).Error("parse ref failed")
		return nil, errors.Wrapf(err, "parse image reference %s", ref)
	}

	var u, p string
	var keychain *PassKeyChain

	for _, cred := range Credentials {
		if username, secret, err := cred(host, refSpec); err != nil {
			return nil, err
		} else if !(username == "" && secret == "") {
			u = username
			p = secret

			keychain = &PassKeyChain{
				Username: u,
				Password: p,
			}

			break
		}
	}

	return keychain, nil
}

// from stargz-snapshotter/service/keychain/cri/cri.go
func parseReference(ref string) (reference.Spec, error) {
	namedRef, err := distribution.ParseDockerRef(ref)
	if err != nil {
		return reference.Spec{}, fmt.Errorf("failed to parse image reference %q: %w", ref, err)
	}
	return reference.Parse(namedRef.String())
}
