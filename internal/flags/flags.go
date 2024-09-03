/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package flags

import (
	"github.com/containerd/nydus-snapshotter/internal/constant"
	"github.com/urfave/cli/v2"
)

type Args struct {
	Address               string
	NydusdConfigPath      string
	SnapshotterConfigPath string
	RootDir               string
	NydusdPath            string
	NydusImagePath        string
	NydusOverlayFSPath    string
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
			DefaultText: constant.DefaultRootDir,
		},
		&cli.StringFlag{
			Name:        "address",
			Usage:       "remote snapshotter gRPC socket path",
			Destination: &args.Address,
			DefaultText: constant.DefaultAddress,
		},
		&cli.StringFlag{
			Name:        "config",
			Usage:       "path to nydus-snapshotter configuration (such as: config.toml)",
			Destination: &args.SnapshotterConfigPath,
		},
		&cli.StringFlag{
			Name:        "nydus-image",
			Usage:       "path to `nydus-image` binary, default to search in $PATH (such as: /usr/local/bin/nydus-image)",
			Destination: &args.NydusImagePath,
		},
		&cli.StringFlag{
			Name:        "nydusd",
			Usage:       "path to `nydusd` binary, default to search in $PATH (such as: /usr/local/bin/nydusd)",
			Destination: &args.NydusdPath,
		},
		&cli.StringFlag{
			Name:        "nydusd-config",
			Aliases:     []string{"config-path"},
			Usage:       "path to nydusd configuration (such as: nydusd-config.json or nydusd-config-v2.toml)",
			Destination: &args.NydusdConfigPath,
			DefaultText: constant.DefaultNydusDaemonConfigPath,
		},
		&cli.StringFlag{
			Name:        "nydus-overlayfs-path",
			Usage:       "path of nydus-overlayfs or name of binary from $PATH, defaults to 'nydus-overlayfs'",
			Destination: &args.NydusOverlayFSPath,
		},
		&cli.StringFlag{
			Name:        "daemon-mode",
			Usage:       "nydusd daemon working mode, possible values: \"dedicated\", \"multiple\", \"shared\" or \"none\". \"multiple\" is an alias of \"dedicated\" and will be deprecated in v1.0",
			Destination: &args.DaemonMode,
			DefaultText: constant.DaemonModeMultiple,
		},
		&cli.StringFlag{
			Name:        "fs-driver",
			Usage:       "driver to mount RAFS filesystem, possible values: \"fusedev\", \"fscache\"",
			Destination: &args.FsDriver,
			DefaultText: constant.FsDriverFusedev,
		},
		&cli.StringFlag{
			Name:        "log-level",
			Usage:       "logging level, possible values: \"trace\", \"debug\", \"info\", \"warn\", \"error\"",
			Destination: &args.LogLevel,
			DefaultText: constant.DefaultLogLevel,
		},
		&cli.BoolFlag{
			Name:        "log-to-stdout",
			Usage:       "print log messages to standard output",
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
