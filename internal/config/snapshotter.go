/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package config

import (
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml"
	"github.com/pkg/errors"
	exec "golang.org/x/sys/execabs"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/internal/containerd-nydus-grpc/command"
	"github.com/containerd/nydus-snapshotter/internal/containerd-nydus-grpc/logging"
)

var SnapshotsDir string

type SnapshotterConfig struct {
	Snapshotter command.Args `toml:"snapshotter"`
}

func LoadSnapshotterConfig(path string) (*SnapshotterConfig, error) {
	tree, err := toml.LoadFile(path)
	if err != nil {
		return nil, errors.Wrapf(err, "load snapshotter configuration file %q", path)
	}

	var config SnapshotterConfig
	if err = tree.Unmarshal(config); err != nil {
		return nil, errors.Wrapf(err, "unmarshal snapshotter configuration file %q", path)
	}

	return &config, nil
}

func SetStartupParameter(args *command.Args, cfg *Config) error {
	if args == nil {
		return errors.New("no startup parameter provided")
	}

	if args.ValidateSignature {
		if args.PublicKeyFile == "" {
			return errors.New("need to specify publicKey file for signature validation")
		} else if _, err := os.Stat(args.PublicKeyFile); err != nil {
			return errors.Wrapf(err, "failed to find publicKey file %q", args.PublicKeyFile)
		}
	}
	cfg.PublicKeyFile = args.PublicKeyFile
	cfg.ValidateSignature = args.ValidateSignature
	cfg.DaemonCfgPath = args.ConfigPath

	// Give --shared-daemon higher priority
	cfg.DaemonMode = args.DaemonMode
	if args.SharedDaemon {
		cfg.DaemonMode = command.DaemonModeShared
	}

	if args.FsDriver == FsDriverFscache && args.DaemonMode != command.DaemonModeShared {
		return errors.New("`fscache` driver only supports `shared` daemon mode")
	}

	cfg.RootDir = args.RootDir
	if len(cfg.RootDir) == 0 {
		return errors.New("invalid empty root directory")
	}

	// Snapshots does not have to bind to any runtime daemon.
	SnapshotsDir = path.Join(cfg.RootDir, "snapshots")

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
	cfg.APISocket = args.APISocket
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

func (c *Config) FillUpWithDefaults() error {
	if c.LogLevel == "" {
		c.LogLevel = DefaultLogLevel
	}
	if c.DaemonCfgPath == "" {
		c.DaemonCfgPath = defaultNydusDaemonConfigPath
	}

	if c.DaemonMode == "" {
		c.DaemonMode = command.DefaultDaemonMode
	}

	if c.GCPeriod == 0 {
		c.GCPeriod = defaultGCPeriod
	}

	if len(c.CacheDir) == 0 {
		c.CacheDir = filepath.Join(c.RootDir, "cache")
	}

	if len(c.LogDir) == 0 {
		c.LogDir = filepath.Join(c.RootDir, logging.DefaultLogDirName)
	}

	return c.SetupNydusBinaryPaths()
}

func (c *Config) SetupNydusBinaryPaths() error {
	// when using DaemonMode = none, nydusd and nydus-image binaries are not required
	if c.DaemonMode == command.DaemonModeNone {
		return nil
	}

	// resolve nydusd path
	if c.NydusdBinaryPath == "" {
		path, err := exec.LookPath(nydusdBinaryName)
		if err != nil {
			return err
		}
		c.NydusdBinaryPath = path
	}

	// resolve nydus-image path
	if c.NydusImageBinaryPath == "" {
		path, err := exec.LookPath(nydusImageBinaryName)
		if err != nil {
			return err
		}
		c.NydusImageBinaryPath = path
	}

	return nil
}
