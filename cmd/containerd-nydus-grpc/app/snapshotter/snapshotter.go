/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshotter

import (
	"context"

	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/utils/signals"
	"github.com/containerd/nydus-snapshotter/snapshot"
)

func Start(ctx context.Context, cfg *config.SnapshotterConfig) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	rs, err := snapshot.NewSnapshotter(ctx, cfg)
	if err != nil {
		return errors.Wrap(err, "failed to initialize snapshotter")
	}

	stopSignal := signals.SetupSignalHandler()
	opt := ServeOptions{
		ListeningSocketPath: cfg.Address,
		EnableCRIKeychain:   cfg.RemoteConfig.AuthConfig.EnableCRIKeychain,
		ImageServiceAddress: cfg.RemoteConfig.AuthConfig.ImageServiceAddress,
	}

	if cfg.RemoteConfig.AuthConfig.EnableKubeconfigKeychain {
		if err := auth.InitKubeSecretListener(ctx, cfg.RemoteConfig.AuthConfig.KubeconfigPath); err != nil {
			return err
		}
	}

	return Serve(ctx, rs, opt, stopSignal)
}
