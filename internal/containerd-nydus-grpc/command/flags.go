/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package command

import (
	"time"

	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/slices"
	"github.com/urfave/cli/v2"
)

// Define a policy how to fork nydusd daemon and attach file system instances to serve.
type Args struct {
	Address                  string
	LogLevel                 string
	LogDir                   string
	ConfigPath               string
	DaemonConfigPath         string
	RootDir                  string
	CacheDir                 string
	GCPeriod                 time.Duration
	ValidateSignature        bool
	PublicKeyFile            string
	ConvertVpcRegistry       bool
	NydusdBinaryPath         string
	NydusImageBinaryPath     string
	SharedDaemon             bool
	DaemonMode               DaemonMode
	FsDriver                 string
	SyncRemove               bool
	EnableMetrics            bool
	MetricsFile              string
	EnableStargz             bool
	DisableCacheManager      bool
	LogToStdout              bool
	EnableNydusOverlayFS     bool
	NydusdThreadNum          int
	CleanupOnClose           bool
	KubeconfigPath           string
	EnableKubeconfigKeychain bool
	RecoverPolicy            string
	PrintVersion             bool
	APISocket                string
}

// Define a policy how to fork nydusd daemon and attach file system instances to serve.
type DaemonMode string

const (
	// One nydusd, one rafs instance.
	DaemonModeMultiple DaemonMode = "multiple"

	// One nydusd serves multiple rafs instances.
	DaemonModeShared DaemonMode = "shared"

	// No nydusd daemon is needed to be started. Snapshotter does not start any nydusd
	// and only interacts with containerd with mount slice to pass necessary configuration
	// to container runtime.
	DaemonModeNone DaemonMode = "none"
)

// UnmarshalText implements the toml.UnmarshalText func.
func (d *DaemonMode) UnmarshalText(b []byte) error {
	*d = DaemonMode(b)
	return d.validate()
}

// MarshalText implements the toml.MarshalText func.
func (d *DaemonMode) MarshalText() ([]byte, error) {
	return []byte(string(*d)), nil
}

// Validate parameters.
func (d *DaemonMode) validate() error {
	if !slices.Contains([]DaemonMode{DaemonModeMultiple, DaemonModeShared, DaemonModeNone}, *d) {
		return errdefs.ErrInvalidArgument
	}

	return nil
}

// Set implements the cli.Generic interface.
func (d *DaemonMode) Set(value string) error {
	*d = DaemonMode(value)
	return nil
}

// String implements the cli.Generic interface.
func (d *DaemonMode) String() string {
	return string(*d)
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
			Name:        "config",
			Value:       DefaultConfigPath,
			Usage:       "load nydus-snapshotter configuration from the specified file, default is /etc/nydus-snapshotter/config.toml",
			Destination: &args.ConfigPath,
		},
		&cli.StringFlag{
			Name:        "daemon-config",
			Value:       DefaultDaemonConfigPath,
			Aliases:     []string{"config-path"},
			Usage:       "load daemon configuration from the specified file, default is /etc/nydus/config.json",
			Destination: &args.DaemonConfigPath,
		},
		&cli.BoolFlag{
			Name:        "convert-vpc-registry",
			Usage:       "whether to automatically convert the image to vpc registry to accelerate image pulling",
			Destination: &args.ConvertVpcRegistry,
		},
		&cli.GenericFlag{
			Name:        "daemon-mode",
			Value:       &args.DaemonMode,
			Aliases:     []string{"M"},
			Usage:       "set daemon working `MODE`, one of \"multiple\", \"shared\" or \"none\"",
			DefaultText: string(DefaultDaemonMode),
		},
		&cli.BoolFlag{
			Name:        "disable-cache-manager",
			Usage:       "whether to disable blob cache manager",
			Destination: &args.DisableCacheManager,
		},
		&cli.BoolFlag{
			Name:        "enable-metrics",
			Usage:       "whether to collect metrics",
			Destination: &args.EnableMetrics,
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
		&cli.DurationFlag{
			Name:        "gc-period",
			Value:       DefaultGCPeriod,
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
			Value:       DefaultLogLevel.String(),
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
			Name:        "metrics-file",
			Usage:       "path to the metrics output `FILE`",
			Destination: &args.MetricsFile,
		},
		&cli.StringFlag{
			Name:        "nydus-image",
			Value:       "",
			Aliases:     []string{"nydusimg-path"},
			Usage:       "set `PATH` to the nydus-image binary, default to lookup nydus-image in $PATH",
			Destination: &args.NydusImageBinaryPath,
		},
		&cli.StringFlag{
			Name:        "nydusd",
			Value:       "",
			Aliases:     []string{"nydusd-path"},
			Usage:       "set `PATH` to the nydusd binary, default to lookup nydusd in $PATH",
			Destination: &args.NydusdBinaryPath,
		},
		&cli.IntFlag{
			Name:        "nydusd-thread-num",
			Usage:       "set worker thread number for nydusd, default to the number of CPUs",
			Destination: &args.NydusdThreadNum,
		},
		&cli.StringFlag{
			Name:        "publickey-file",
			Value:       defaultPublicKey,
			Usage:       "path to the publickey `FILE` for signature validation",
			Destination: &args.PublicKeyFile,
		},
		&cli.StringFlag{
			Name:        "root",
			Value:       DefaultRootDir,
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
