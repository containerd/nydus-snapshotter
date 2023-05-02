/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package flags

import (
	"github.com/urfave/cli/v2"
)

type Args struct {
	Address               string
	NydusdConfigPath      string
	SnapshotterConfigPath string
	RootDir               string
	NydusdPath            string
	NydusImagePath        string
	DaemonMode            string
	FsDriver              string
	LogLevel              string
	LogToStdout           bool
	LogToStdoutCount      int
	PrintVersion          bool
}

type Flags struct {
	Args *Args
	F    []cli.Flag
}

func buildFlags(args *Args) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "root",
			Usage:       "directory to store snapshotter data and working states",
			Destination: &args.RootDir,
		},
		&cli.StringFlag{
			Name:        "address",
			Usage:       "remote snapshotter gRPC socket path",
			Destination: &args.Address,
		},
		&cli.StringFlag{
			Name:        "config",
			Usage:       "path to nydus-snapshotter configuration file",
			Destination: &args.SnapshotterConfigPath,
		},
		&cli.StringFlag{
			Name:        "nydus-image",
			Usage:       "path to `nydus-image` binary, default to search in $PATH",
			Destination: &args.NydusImagePath,
		},
		&cli.StringFlag{
			Name:        "nydusd",
			Usage:       "path to `nydusd` binary, default to search in $PATH",
			Destination: &args.NydusdPath,
		},
		&cli.StringFlag{
			Name:        "nydusd-config",
			Aliases:     []string{"config-path"},
			Usage:       "path to nydusd configuration file",
			Destination: &args.NydusdConfigPath,
		},
		&cli.StringFlag{
			Name:        "daemon-mode",
			Usage:       "nydusd daemon working mode, possible values: \"multiple\", \"shared\" or \"none\"",
			Destination: &args.DaemonMode,
		},
		&cli.StringFlag{
			Name:        "fs-driver",
			Usage:       "driver to mount RAFS filesystem, possible values: \"fusedev\", \"fscache\"",
			Destination: &args.FsDriver,
		},
		&cli.StringFlag{
			Name:        "log-level",
			Usage:       "logging level, possible values: \"trace\", \"debug\", \"info\", \"warn\", \"error\"",
			Destination: &args.LogLevel,
		},
		&cli.BoolFlag{
			Name:        "log-to-stdout",
			Usage:       "print log messages to STDOUT",
			Destination: &args.LogToStdout,
			Count:       &args.LogToStdoutCount,
		},
		&cli.BoolFlag{
			Name:        "version",
			Usage:       "print version and build information",
			Destination: &args.PrintVersion,
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
