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
	ConfigPath            string
	SnapshotterConfigPath string
	RootDir               string
	NydusdPath            string
	NydusImagePath        string
	SharedDaemon          bool
	DaemonMode            string
	FsDriver              string
	MetricsAddress        string
	LogToStdout           bool
	RecoverPolicy         string
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
			Name:        "address",
			Value:       defaultAddress,
			Usage:       "set `PATH` for gRPC socket",
			Destination: &args.Address,
		},
		&cli.StringFlag{
			Name:        "config-path",
			Aliases:     []string{"nydusd-config"},
			Usage:       "path to the nydusd configuration",
			Destination: &args.ConfigPath,
		},
		&cli.StringFlag{
			Name:        "config",
			Usage:       "path to the nydus-snapshotter configuration",
			Destination: &args.SnapshotterConfigPath,
		},
		&cli.StringFlag{
			Name:        "daemon-mode",
			Value:       DefaultDaemonMode,
			Aliases:     []string{"M"},
			Usage:       "set daemon working `MODE`, one of \"multiple\", \"shared\" or \"none\"",
			Destination: &args.DaemonMode,
		},
		&cli.StringFlag{
			Name:        "metrics-address",
			Value:       "",
			Usage:       "Enable metrics server by setting to an `ADDRESS` such as \"localhost:8080\", \":8080\"",
			Destination: &args.MetricsAddress,
		},
		&cli.StringFlag{
			Name:        "fs-driver",
			Value:       FsDriverFusedev,
			Aliases:     []string{"daemon-backend"},
			Usage:       "backend `DRIVER` to serve the filesystem, one of \"fusedev\", \"fscache\"",
			Destination: &args.FsDriver,
		},
		&cli.StringFlag{
			Name:        "log-level",
			Value:       defaultLogLevel.String(),
			Aliases:     []string{"l"},
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
			Aliases:     []string{"nydusimg-path"},
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

		&cli.StringFlag{
			Name:        "root",
			Value:       defaultRootDir,
			Aliases:     []string{"R"},
			Usage:       "set `DIRECTORY` to store snapshotter working state",
			Destination: &args.RootDir,
		},
		&cli.BoolFlag{
			Name:        "shared-daemon",
			Usage:       "Deprecated, equivalent to \"--daemon-mode shared\"",
			Destination: &args.SharedDaemon,
		},
		&cli.StringFlag{
			Name:        "recover-policy",
			Usage:       "Policy on recovering nydus filesystem service [none, restart, failover], default to restart",
			Destination: &args.RecoverPolicy,
			Value:       "restart",
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
