/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"os"

	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
	"github.com/urfave/cli/v2"

	"github.com/containerd/nydus-snapshotter/cmd/containerd-nydus-grpc/app/snapshotter"
	"github.com/containerd/nydus-snapshotter/cmd/containerd-nydus-grpc/pkg/command"
	"github.com/containerd/nydus-snapshotter/cmd/containerd-nydus-grpc/pkg/logging"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
)

func main() {
	flags := command.NewFlags()
	app := &cli.App{
		Name:    "containerd-nydus-grpc",
		Usage:   "Nydus remote snapshotter for containerd",
		Version: Version,
		Flags:   flags.F,
		Action: func(c *cli.Context) error {
			var cfg config.Config
			if err := command.Validate(flags.Args, &cfg); err != nil {
				return errors.Wrap(err, "invalid argument")
			}

			ctx := logging.WithContext()
			logRotateArgs := &logging.RotateLogArgs{
				RotateLogMaxSize:    cfg.RotateLogMaxSize,
				RotateLogMaxBackups: cfg.RotateLogMaxBackups,
				RotateLogMaxAge:     cfg.RotateLogMaxAge,
				RotateLogLocalTime:  cfg.RotateLogLocalTime,
				RotateLogCompress:   cfg.RotateLogCompress,
			}
			if err := logging.SetUp(flags.Args.LogLevel, flags.Args.LogToStdout, flags.Args.LogDir, flags.Args.RootDir, logRotateArgs); err != nil {
				return errors.Wrap(err, "failed to set up logger")
			}

			return snapshotter.Start(ctx, cfg)
		},
	}
	if err := app.Run(os.Args); err != nil {
		if errdefs.IsConnectionClosed(err) {
			log.L.Info("nydus-snapshotter exited")
		} else {
			log.L.WithError(err).Fatal("failed to start nydus-snapshotter")
		}
	}
}
