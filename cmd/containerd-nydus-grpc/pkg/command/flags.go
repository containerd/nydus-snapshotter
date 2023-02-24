/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package command

import (
	"github.com/urfave/cli/v2"
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
			Usage:       "print version and build information",
			Destination: &args.PrintVersion,
		},
		&cli.StringFlag{
			Name:        "root",
			Usage:       "the directory storing snapshotter working states",
			Destination: &args.RootDir,
		},
		&cli.StringFlag{
			Name:        "address",
			Usage:       "gRPC socket path",
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
			Usage:       "spawning nydusd daemon mode, legal values include \"multiple\", \"shared\" or \"none\"",
			Destination: &args.DaemonMode,
		},
		&cli.StringFlag{
			Name:        "fs-driver",
			Usage:       "fulfill image service based on what fs driver, possible values include \"fusedev\", \"fscache\"",
			Destination: &args.FsDriver,
		},
		&cli.StringFlag{
			Name:        "log-level",
			Usage:       "logging level, possible values \"trace\", \"debug\", \"info\", \"warn\", \"error\"",
			Destination: &args.LogLevel,
		},
		&cli.BoolFlag{
			Name:        "log-to-stdout",
			Usage:       "print log messages to STDOUT",
			Destination: &args.LogToStdout,
		},
		&cli.StringFlag{
			Name:        "nydus-image",
			Usage:       "`nydus-image` binary path, it will be searched from $PATH if this option is not provided",
			Destination: &args.NydusImagePath,
		},
		&cli.StringFlag{
			Name:        "nydusd",
			Usage:       "`nydusd` binary path, it will be searched if this option is not provided",
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
