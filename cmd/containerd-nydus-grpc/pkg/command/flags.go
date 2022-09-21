/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package command

import (
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/cmd/containerd-nydus-grpc/pkg/logging"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

const (
	defaultAddress             = "/run/containerd-nydus/containerd-nydus-grpc.sock"
	defaultLogLevel            = logrus.InfoLevel
	defaultRootDir             = "/var/lib/containerd-nydus"
	oldDefaultRootDir          = "/var/lib/containerd-nydus-grpc"
	defaultGCPeriod            = "24h"
	defaultPublicKey           = "/signing/nydus-image-signing-public.key"
	defaultRotateLogMaxSize    = 200 // 200 megabytes
	defaultRotateLogMaxBackups = 10
	defaultRotateLogMaxAge     = 0 // days
	defaultRotateLogLocalTime  = true
	defaultRotateLogCompress   = true
)

type Args struct {
	Address                  string
	LogLevel                 string
	LogDir                   string
	ConfigPath               string
	RootDir                  string
	CacheDir                 string
	GCPeriod                 string
	ValidateSignature        bool
	PublicKeyFile            string
	ConvertVpcRegistry       bool
	NydusdBinaryPath         string
	NydusImageBinaryPath     string
	SharedDaemon             bool
	DaemonMode               string
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
}

type Flags struct {
	Args *Args
	F    []cli.Flag
}

func buildFlags(args *Args) []cli.Flag {
	return []cli.Flag{
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
			Required:    true,
			Aliases:     []string{"c", "config"},
			Usage:       "path to the configuration `FILE`",
			Destination: &args.ConfigPath,
		},
		&cli.BoolFlag{
			Name:        "convert-vpc-registry",
			Usage:       "whether to automatically convert the image to vpc registry to accelerate image pulling",
			Destination: &args.ConvertVpcRegistry,
		},
		&cli.StringFlag{
			Name:        "daemon-mode",
			Value:       config.DefaultDaemonMode,
			Aliases:     []string{"M"},
			Usage:       "set daemon working `MODE`, one of \"multiple\", \"shared\" or \"none\"",
			Destination: &args.DaemonMode,
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
			Value:       config.FsDriverFusedev,
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
	}
}

func NewFlags() *Flags {
	var args Args
	return &Flags{
		Args: &args,
		F:    buildFlags(&args),
	}
}

func Validate(args *Args, cfg *config.Config) error {
	var daemonCfg config.DaemonConfig
	if err := config.LoadConfig(args.ConfigPath, &daemonCfg); err != nil {
		return errors.Wrapf(err, "failed to load config file %q", args.ConfigPath)
	}
	cfg.DaemonCfg = daemonCfg

	if args.ValidateSignature {
		if args.PublicKeyFile == "" {
			return errors.New("need to specify publicKey file for signature validation")
		} else if _, err := os.Stat(args.PublicKeyFile); err != nil {
			return errors.Wrapf(err, "failed to find publicKey file %q", args.PublicKeyFile)
		}
	}
	cfg.PublicKeyFile = args.PublicKeyFile
	cfg.ValidateSignature = args.ValidateSignature

	// Give --shared-daemon higher priority
	cfg.DaemonMode = args.DaemonMode
	if args.SharedDaemon {
		cfg.DaemonMode = config.DaemonModeShared
	}
	if args.FsDriver == config.FsDriverFscache && args.DaemonMode != config.DaemonModeShared {
		return errors.New("`fscache` driver only supports `shared` daemon mode")
	}

	cfg.RootDir = args.RootDir
	if len(cfg.RootDir) == 0 {
		return errors.New("invalid empty root directory")
	}

	if args.RootDir == defaultRootDir {
		if entries, err := os.ReadDir(oldDefaultRootDir); err == nil {
			if len(entries) != 0 {
				log.L.Warnf("Default root directory is changed to %s", defaultRootDir)
			}
		}
	}

	cfg.CacheDir = args.CacheDir
	if len(cfg.CacheDir) == 0 {
		cfg.CacheDir = filepath.Join(cfg.RootDir, "cache")
	}

	cfg.LogLevel = args.LogLevel
	// Always let options from CLI override those from configuration file.
	cfg.LogToStdout = args.LogToStdout
	cfg.LogDir = args.LogDir
	if len(cfg.LogDir) == 0 {
		cfg.LogDir = filepath.Join(cfg.RootDir, logging.DefaultLogDirName)
	}
	cfg.RotateLogMaxSize = defaultRotateLogMaxSize
	cfg.RotateLogMaxBackups = defaultRotateLogMaxBackups
	cfg.RotateLogMaxAge = defaultRotateLogMaxAge
	cfg.RotateLogLocalTime = defaultRotateLogLocalTime
	cfg.RotateLogCompress = defaultRotateLogCompress

	d, err := time.ParseDuration(args.GCPeriod)
	if err != nil {
		return errors.Wrapf(err, "parse gc period %v failed", args.GCPeriod)
	}
	cfg.GCPeriod = d

	cfg.Address = args.Address
	cfg.CleanupOnClose = args.CleanupOnClose
	cfg.ConvertVpcRegistry = args.ConvertVpcRegistry
	cfg.DisableCacheManager = args.DisableCacheManager
	cfg.EnableMetrics = args.EnableMetrics
	cfg.EnableStargz = args.EnableStargz
	cfg.EnableNydusOverlayFS = args.EnableNydusOverlayFS
	cfg.FsDriver = args.FsDriver
	cfg.MetricsFile = args.MetricsFile
	cfg.NydusdBinaryPath = args.NydusdBinaryPath
	cfg.NydusImageBinaryPath = args.NydusImageBinaryPath
	cfg.NydusdThreadNum = args.NydusdThreadNum
	cfg.SyncRemove = args.SyncRemove
	cfg.KubeconfigPath = args.KubeconfigPath
	cfg.EnableKubeconfigKeychain = args.EnableKubeconfigKeychain
	cfg.RecoverPolicy = args.RecoverPolicy

	return cfg.SetupNydusBinaryPaths()
}
