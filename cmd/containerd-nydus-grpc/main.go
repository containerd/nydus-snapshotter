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

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/internal/flags"
	"github.com/containerd/nydus-snapshotter/internal/logging"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/version"
)

func main() {
	flags := flags.NewFlags()
	app := &cli.App{
		Name:        "containerd-nydus-grpc",
		Usage:       "Nydus remote snapshotter for containerd",
		Version:     version.Version,
		Flags:       flags.F,
		HideVersion: true,
		Action: func(c *cli.Context) error {
			if flags.Args.PrintVersion {
				fmt.Println("Version:    ", version.Version)
				fmt.Println("Revision:   ", version.Revision)
				fmt.Println("Go version: ", version.GoVersion)
				fmt.Println("Build time: ", version.BuildTimestamp)
				return nil
			}

			snapshotterConfigPath := flags.Args.SnapshotterConfigPath
			var defaultSnapshotterConfig config.SnapshotterConfig
			var snapshotterConfig config.SnapshotterConfig

			if err := defaultSnapshotterConfig.FillUpWithDefaults(); err != nil {
				return errors.New("failed to fill up nydus configuration with defaults")
			}

			// Once snapshotter's configuration file is provided, parse it and let command line parameters override it.
			if snapshotterConfigPath != "" {
				if c, err := config.LoadSnapshotterConfig(snapshotterConfigPath); err == nil {
					// Command line parameters override the snapshotter's configurations for backwards compatibility
					if err := config.ParseParameters(flags.Args, c); err != nil {
						return errors.Wrap(err, "parse parameters")
					}
					snapshotterConfig = *c
				} else {
					return errors.Wrapf(err, "Failed to load snapshotter's configuration at %q", snapshotterConfigPath)
				}
			} else {
				if err := config.ParseParameters(flags.Args, &snapshotterConfig); err != nil {
					return errors.Wrap(err, "parse parameters")
				}
			}

			if err := config.MergeConfig(&snapshotterConfig, &defaultSnapshotterConfig); err != nil {
				return errors.Wrap(err, "merge configurations")
			}

			if err := config.ValidateConfig(&snapshotterConfig); err != nil {
				return errors.Wrapf(err, "validate configurations")
			}

			if err := config.ProcessConfigurations(&snapshotterConfig); err != nil {
				return errors.Wrap(err, "process configurations")
			}

			if err := config.SetUpEnvironment(&snapshotterConfig); err != nil {
				return errors.Wrap(err, "setup environment failed")
			}

			ctx := logging.WithContext()
			logConfig := &snapshotterConfig.LoggingConfig
			logRotateArgs := &logging.RotateLogArgs{
				RotateLogMaxSize:    logConfig.RotateLogMaxSize,
				RotateLogMaxBackups: logConfig.RotateLogMaxBackups,
				RotateLogMaxAge:     logConfig.RotateLogMaxAge,
				RotateLogLocalTime:  logConfig.RotateLogLocalTime,
				RotateLogCompress:   logConfig.RotateLogCompress,
			}

			if err := logging.SetUp(logConfig.LogLevel, logConfig.LogToStdout, logConfig.LogDir, logRotateArgs); err != nil {
				return errors.Wrap(err, "set up logger")
			}

			log.L.Infof("Start nydus-snapshotter. PID %d Version %s FsDriver %s DaemonMode %s",
				os.Getpid(), version.Version, config.GetFsDriver(), snapshotterConfig.DaemonMode)

			return Start(ctx, &snapshotterConfig)
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
