/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package command

import (
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

const (
	defaultAddress           = "/run/containerd-nydus/containerd-nydus-grpc.sock"
	defaultLogLevel          = logrus.InfoLevel
	defaultRootDir           = "/var/lib/containerd-nydus"
	defaultGCPeriod          = "24h"
	defaultPublicKey         = "/signing/nydus-image-signing-public.key"
	DefaultDaemonMode string = "multiple"
	FsDriverFusedev   string = "fusedev"
)

type Args struct {
	Address                  string
	LogLevel                 string
	LogDir                   string
	ConfigPath               string
	SnapshotterConfigPath    string
	RootDir                  string
	CacheDir                 string
	GCPeriod                 string
	ValidateSignature        bool
	PublicKeyFile            string
	ConvertVpcRegistry       bool
	NydusdPath               string
	NydusImagePath           string
	SharedDaemon             bool
	DaemonMode               string
	FsDriver                 string
	SyncRemove               bool
	MetricsAddress           string
	EnableStargz             bool
	DisableCacheManager      bool
	LogToStdout              bool
	EnableNydusOverlayFS     bool
	NydusdThreadsNumber      int
	CleanupOnClose           bool
	KubeconfigPath           string
	EnableKubeconfigKeychain bool
	RecoverPolicy            string
	PrintVersion             bool
	EnableSystemController   bool
	EnableCRIKeychain        bool
	ImageServiceAddress      string
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
			Name:        "cache-dir",
			Value:       "",
			Aliases:     []string{"C"},
			Usage:       "set `DIRECTORY` to store/cache downloaded image data",
			Destination: &args.CacheDir,
		},
		&cli.BoolFlag{
			Name:        "cleanup-on-close",
			Value:       false,
			Usage:       "whether to clean up on exit",
			Destination: &args.CleanupOnClose,
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
		&cli.BoolFlag{
			Name:        "convert-vpc-registry",
			Usage:       "whether to automatically convert the image to vpc registry to accelerate image pulling",
			Destination: &args.ConvertVpcRegistry,
		},
		&cli.StringFlag{
			Name:        "daemon-mode",
			Value:       DefaultDaemonMode,
			Aliases:     []string{"M"},
			Usage:       "set daemon working `MODE`, one of \"multiple\", \"shared\" or \"none\"",
			Destination: &args.DaemonMode,
		},
		&cli.BoolFlag{
			Name:        "disable-cache-manager",
			Usage:       "whether to disable blob cache manager",
			Destination: &args.DisableCacheManager,
		},
		&cli.StringFlag{
			Name:        "metrics-address",
			Value:       "",
			Usage:       "Enable metrics server by setting to an `ADDRESS` such as \"localhost:8080\", \":8080\"",
			Destination: &args.MetricsAddress,
		},
		&cli.BoolFlag{
			Name:        "enable-nydus-overlayfs",
			Usage:       "whether to enable nydus-overlayfs",
			Destination: &args.EnableNydusOverlayFS,
		},
		&cli.BoolFlag{
			Name:        "enable-stargz",
			Usage:       "whether to enable support of estargz image (experimental)",
			Destination: &args.EnableStargz,
		},
		&cli.StringFlag{
			Name:        "fs-driver",
			Value:       FsDriverFusedev,
			Aliases:     []string{"daemon-backend"},
			Usage:       "backend `DRIVER` to serve the filesystem, one of \"fusedev\", \"fscache\"",
			Destination: &args.FsDriver,
		},
		&cli.StringFlag{
			Name:        "gc-period",
			Value:       defaultGCPeriod,
			Usage:       "blob cache garbage collection `INTERVAL`, duration string(for example, 1m, 2h)",
			Destination: &args.GCPeriod,
		},
		&cli.StringFlag{
			Name:        "log-dir",
			Value:       "",
			Aliases:     []string{"L"},
			Usage:       "set `DIRECTORY` to store log files",
			Destination: &args.LogDir,
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
		&cli.IntFlag{
			Name:        "nydusd-thread-num",
			Usage:       "set worker thread number for nydusd, default to the number of CPUs",
			Destination: &args.NydusdThreadsNumber,
		},
		&cli.StringFlag{
			Name:        "publickey-file",
			Value:       defaultPublicKey,
			Usage:       "path to the publickey `FILE` for signature validation",
			Destination: &args.PublicKeyFile,
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
		&cli.BoolFlag{
			Name:        "sync-remove",
			Usage:       "whether to clean up snapshots in synchronous mode, default to asynchronous mode",
			Destination: &args.SyncRemove,
		},
		&cli.BoolFlag{
			Name:        "validate-signature",
			Usage:       "whether to validate integrity of image bootstrap",
			Destination: &args.ValidateSignature,
		},
		&cli.BoolFlag{
			Name:        "enable-system-controller",
			Usage:       "(experimental) unix domain socket path to serve HTTP-based system management",
			Destination: &args.EnableSystemController,
			Value:       true,
		},
		&cli.StringFlag{
			Name:        "kubeconfig-path",
			Value:       "",
			Usage:       "path to the kubeconfig file",
			Destination: &args.KubeconfigPath,
		},
		&cli.BoolFlag{
			Name:        "enable-kubeconfig-keychain",
			Value:       false,
			Usage:       "synchronize `kubernetes.io/dockerconfigjson` secret from kubernetes API server with provided `--kubeconfig-path` (default `$KUBECONFIG` or `~/.kube/config`)",
			Destination: &args.EnableKubeconfigKeychain,
		},
		&cli.BoolFlag{
			Name:        "enable-cri-keychain",
			Value:       false,
			Usage:       "enable a CRI image proxy and retrieve image secret when proxying image request",
			Destination: &args.EnableCRIKeychain,
		},
		&cli.StringFlag{
			Name:        "image-service-address",
			Value:       auth.DefaultImageServiceAddress,
			Usage:       "the target image service when using image proxy",
			Destination: &args.ImageServiceAddress,
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
