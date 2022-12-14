/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshotter

import (
	"context"
	"net"
	"os"
	"path/filepath"

	api "github.com/containerd/containerd/api/services/snapshots/v1"
	"github.com/containerd/containerd/contrib/snapshotservice"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots"
	"github.com/pkg/errors"
	"google.golang.org/grpc"

	"github.com/containerd/nydus-snapshotter/pkg/auth"
)

type ServeOptions struct {
	ListeningSocketPath string
	EnableCRIKeychain   bool
	ImageServiceAddress string
}

func Serve(ctx context.Context, sn snapshots.Snapshotter, options ServeOptions, stop <-chan struct{}) error {
	err := ensureSocketNotExists(options.ListeningSocketPath)
	if err != nil {
		return err
	}
	rpc := grpc.NewServer()
	if rpc == nil {
		return errors.New("start RPC server")
	}
	api.RegisterSnapshotsServer(rpc, snapshotservice.FromSnapshotter(sn))
	listener, err := net.Listen("unix", options.ListeningSocketPath)
	if err != nil {
		return errors.Wrapf(err, "listen socket %q", options.ListeningSocketPath)
	}

	if options.EnableCRIKeychain {
		auth.AddImageProxy(ctx, rpc, options.ImageServiceAddress)
	}

	go func() {
		<-stop

		log.L.Infof("Shutting down nydus-snapshotter!")

		if err := sn.Close(); err != nil {
			log.L.WithError(err).Errorf("Closing snapshotter error")
		}

		if err := listener.Close(); err != nil {
			log.L.Errorf("failed to close listener %s, err: %v", options.ListeningSocketPath, err)
		}
	}()

	return rpc.Serve(listener)
}

func ensureSocketNotExists(listeningSocketPath string) error {
	if err := os.MkdirAll(filepath.Dir(listeningSocketPath), 0700); err != nil {
		return errors.Wrapf(err, "failed to create directory %q", filepath.Dir(listeningSocketPath))
	}
	finfo, err := os.Stat(listeningSocketPath)
	// err is nil means listening socket path exists, remove before serve
	if err == nil {
		if finfo.Mode()&os.ModeSocket == 0 {
			return errors.Errorf("file %s is not a socket", listeningSocketPath)
		}
		err := os.Remove(listeningSocketPath)
		if err != nil {
			return err
		}
	}
	return nil
}
