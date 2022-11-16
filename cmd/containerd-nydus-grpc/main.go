/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"fmt"
	"os"

	"github.com/containerd/containerd/log"
	"github.com/urfave/cli/v2"

	"github.com/containerd/nydus-snapshotter/cmd/containerd-nydus-grpc/snapshotter"
	"github.com/containerd/nydus-snapshotter/internal/config"
	"github.com/containerd/nydus-snapshotter/internal/containerd-nydus-grpc/command"
	"github.com/containerd/nydus-snapshotter/internal/containerd-nydus-grpc/logging"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
)

func main() {
	flags := command.NewFlags()
	app := &cli.App{
		Name:        "containerd-nydus-grpc",
		Usage:       "Nydus remote snapshotter for containerd",
		Version:     Version,
		Flags:       flags.F,
		HideVersion: true,
		Action: func(c *cli.Context) error {
			if flags.Args.PrintVersion {
				fmt.Println("Version:    ", Version)
				fmt.Println("Reversion:  ", Reversion)
				fmt.Println("Go version: ", GoVersion)
				fmt.Println("Build time: ", BuildTimestamp)
				return nil
			}

			var (
				cfg config.Config
			)
			if flags.Args.ConfigPath != "" {
				snapshotterCfg, err := config.LoadSnapshotterConfig(flags.Args.ConfigPath)
				if err != nil {
					return fmt.Errorf("invalid configuration: %w", err)
				}

				if err = config.SetStartupParameter(&snapshotterCfg.Snapshotter, &cfg); err != nil {
					return fmt.Errorf("invalid configuration: %w", err)
				}
			} else {
				if err := config.SetStartupParameter(flags.Args, &cfg); err != nil {
					return fmt.Errorf("invalid argument: %w", err)
				}
			}

			ctx := logging.WithContext()
			if err := logging.New(flags.Args.LogLevel, flags.Args.LogToStdout, flags.Args.LogDir, flags.Args.RootDir, logging.RotateLogArgs{
				RotateLogMaxSize:    cfg.RotateLogMaxSize,
				RotateLogMaxBackups: cfg.RotateLogMaxBackups,
				RotateLogMaxAge:     cfg.RotateLogMaxAge,
				RotateLogLocalTime:  cfg.RotateLogLocalTime,
				RotateLogCompress:   cfg.RotateLogCompress,
			}); err != nil {
				return fmt.Errorf("failed to set up logger: %w", err)
			}

			log.L.Infof("Start nydus-snapshotter. PID %d Version %s", os.Getpid(), Version)
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
