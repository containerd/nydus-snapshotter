/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package command

import (
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

const (
	defaultAddress           = "/run/containerd-nydus/containerd-nydus-grpc.sock"
	defaultLogLevel          = logrus.InfoLevel
	defaultRootDir           = "/var/lib/containerd-nydus"
	DefaultDaemonMode string = "multiple"
	FsDriverFusedev   string = "fusedev"
)

type Args struct {
	Address               string
	LogLevel              string
	NydusdConfigPath      string
	SnapshotterConfigPath string
	RootDir               string
	NydusdPath            string
	NydusImagePath        string
	DaemonMode            string
	FsDriver              string
	MetricsAddress        string
	LogToStdout           bool
	PrintVersion          bool
}

type Flags struct {
	Args *Args
	F    []cli.Flag
}

func buildFlags(args *Args) []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Name:        "version",
			Value:       false,
			Usage:       "print version and build information",
			Destination: &args.PrintVersion,
		},
		&cli.StringFlag{
			Name:        "root",
			Value:       defaultRootDir,
			Usage:       "set `DIRECTORY` to store snapshotter working state",
			Destination: &args.RootDir,
		},
		&cli.StringFlag{
			Name:        "address",
			Value:       defaultAddress,
			Usage:       "set `PATH` for gRPC socket",
			Destination: &args.Address,
		},
		&cli.StringFlag{
			Name:        "config",
			Usage:       "path to the nydus-snapshotter configuration",
			Destination: &args.SnapshotterConfigPath,
		},
		&cli.StringFlag{
			Name:        "nydusd-config",
			Aliases:     []string{"config-path"},
			Usage:       "path to the nydusd configuration",
			Destination: &args.NydusdConfigPath,
		},
		&cli.StringFlag{
			Name:        "daemon-mode",
			Value:       DefaultDaemonMode,
			Usage:       "set daemon working `MODE`, one of \"multiple\", \"shared\" or \"none\"",
			Destination: &args.DaemonMode,
		},
		&cli.StringFlag{
			Name:        "fs-driver",
			Value:       FsDriverFusedev,
			Usage:       "backend `DRIVER` to serve the filesystem, one of \"fusedev\", \"fscache\"",
			Destination: &args.FsDriver,
		},
		&cli.StringFlag{
			Name:        "log-level",
			Value:       defaultLogLevel.String(),
			Usage:       "set the logging `LEVEL` [trace, debug, info, warn, error, fatal, panic]",
			Destination: &args.LogLevel,
		},
		&cli.BoolFlag{
			Name:        "log-to-stdout",
			Usage:       "log messages to standard out rather than files.",
			Destination: &args.LogToStdout,
		},
		&cli.StringFlag{
			Name:        "nydus-image",
			Value:       "",
			Usage:       "set `PATH` to the nydus-image binary, default to lookup nydus-image in $PATH",
			Destination: &args.NydusImagePath,
		},
		&cli.StringFlag{
			Name:        "nydusd",
			Value:       "",
			Aliases:     []string{"nydusd-path"},
			Usage:       "set `PATH` to the nydusd binary, default to lookup nydusd in $PATH",
			Destination: &args.NydusdPath,
		},
	}
}

func NewFlags() *Flags {
	var args Args
	return &Flags{
		Args: &args,
		F:    buildFlags(&args),
	}
}
