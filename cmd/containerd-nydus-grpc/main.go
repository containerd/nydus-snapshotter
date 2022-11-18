/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"fmt"
	"os"

	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
	"github.com/urfave/cli/v2"

	"github.com/containerd/nydus-snapshotter/cmd/containerd-nydus-grpc/app/snapshotter"
	"github.com/containerd/nydus-snapshotter/cmd/containerd-nydus-grpc/pkg/command"
	"github.com/containerd/nydus-snapshotter/cmd/containerd-nydus-grpc/pkg/logging"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/version"
)

func main() {
	flags := command.NewFlags()
	app := &cli.App{
		Name:        "containerd-nydus-grpc",
		Usage:       "Nydus remote snapshotter for containerd",
		Version:     version.Version,
		Flags:       flags.F,
		HideVersion: true,
		Action: func(c *cli.Context) error {
			if flags.Args.PrintVersion {
				fmt.Println("Version:    ", version.Version)
				fmt.Println("Reversion:  ", version.Reversion)
				fmt.Println("Go version: ", version.GoVersion)
				fmt.Println("Build time: ", version.BuildTimestamp)
				return nil
			}

			var cfg config.Config

			if snapshotterCfg, err := config.LoadSnapshotterConfig(flags.Args.ConfigPath); err == nil && snapshotterCfg != nil {
				if err = config.SetStartupParameter(&snapshotterCfg.StartupFlag, &cfg); err != nil {
					return errors.Wrap(err, "parse parameters")
				}
			} else {
				if err := config.SetStartupParameter(flags.Args, &cfg); err != nil {
					return errors.Wrap(err, "parse parameters")
				}
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
				return errors.Wrap(err, "set up logger")
			}

			log.L.Infof("Start nydus-snapshotter. PID %d Version %s FsDriver %s DaemonMode %s",
				os.Getpid(), version.Version, cfg.FsDriver, cfg.DaemonMode)

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
