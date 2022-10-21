/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package config

import (
	"os"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml"
	"github.com/pkg/errors"
	exec "golang.org/x/sys/execabs"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/cmd/containerd-nydus-grpc/pkg/command"
	"github.com/containerd/nydus-snapshotter/cmd/containerd-nydus-grpc/pkg/logging"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
)

// Define a policy how to fork nydusd daemon and attach file system instances to serve.
type DaemonMode string

const (
	// One nydusd, one rafs instance
	DaemonModeMultiple DaemonMode = "multiple"
	// One nydusd serves multiple rafs instances
	DaemonModeShared DaemonMode = "shared"
	// No nydusd daemon is needed to be started. Snapshotter does not start any nydusd
	// and only interacts with containerd with mount slice to pass necessary configuration
	// to container runtime
	DaemonModeNone DaemonMode = "none"
	// Nydusd daemon is started by serves no snapshot. Nydusd works as a simple blobs downloader to
	// prepare blobcache files locally.
	DaemonModePrefetch DaemonMode = "prefetch"
	DaemonModeInvalid  DaemonMode = ""
)

func parseDaemonMode(m string) (DaemonMode, error) {
	switch m {
	case string(DaemonModeMultiple):
		return DaemonModeMultiple, nil
	case string(DaemonModeShared):
		return DaemonModeShared, nil
	case string(DaemonModeNone):
		return DaemonModeNone, nil
	case string(DaemonModePrefetch):
		return DaemonModePrefetch, nil
	default:
		return DaemonModeInvalid, errdefs.ErrInvalidArgument
	}
}

const (
	DefaultDaemonMode DaemonMode = DaemonModeMultiple

	DefaultLogLevel string = "info"
	defaultGCPeriod        = 24 * time.Hour

	defaultNydusDaemonConfigPath string = "/etc/nydus/config.json"
	nydusdBinaryName             string = "nydusd"
	nydusImageBinaryName         string = "nydus-image"

	defaultRootDir    = "/var/lib/containerd-nydus"
	oldDefaultRootDir = "/var/lib/containerd-nydus-grpc"

	// Log rotation
	defaultRotateLogMaxSize    = 200 // 200 megabytes
	defaultRotateLogMaxBackups = 10
	defaultRotateLogMaxAge     = 0 // days
	defaultRotateLogLocalTime  = true
	defaultRotateLogCompress   = true
)

const (
	FsDriverFusedev string = "fusedev"
	FsDriverFscache string = "fscache"
)

type Config struct {
	Address                  string        `toml:"-"`
	ConvertVpcRegistry       bool          `toml:"-"`
	DaemonCfgPath            string        `toml:"daemon_cfg_path"`
	DaemonCfg                DaemonConfig  `toml:"-"`
	PublicKeyFile            string        `toml:"-"`
	RootDir                  string        `toml:"-"`
	CacheDir                 string        `toml:"cache_dir"`
	GCPeriod                 time.Duration `toml:"gc_period"`
	ValidateSignature        bool          `toml:"validate_signature"`
	NydusdBinaryPath         string        `toml:"nydusd_binary_path"`
	NydusImageBinaryPath     string        `toml:"nydus_image_binary"`
	DaemonMode               DaemonMode    `toml:"daemon_mode"`
	FsDriver                 string        `toml:"daemon_backend"`
	SyncRemove               bool          `toml:"sync_remove"`
	EnableMetrics            bool          `toml:"enable_metrics"`
	MetricsFile              string        `toml:"metrics_file"`
	EnableStargz             bool          `toml:"enable_stargz"`
	LogLevel                 string        `toml:"-"`
	LogDir                   string        `toml:"log_dir"`
	LogToStdout              bool          `toml:"log_to_stdout"`
	DisableCacheManager      bool          `toml:"disable_cache_manager"`
	EnableNydusOverlayFS     bool          `toml:"enable_nydus_overlayfs"`
	NydusdThreadNum          int           `toml:"nydusd_thread_num"`
	CleanupOnClose           bool          `toml:"cleanup_on_close"`
	KubeconfigPath           string        `toml:"kubeconfig_path"`
	EnableKubeconfigKeychain bool          `toml:"enable_kubeconfig_keychain"`
	RotateLogMaxSize         int           `toml:"log_rotate_max_size"`
	RotateLogMaxBackups      int           `toml:"log_rotate_max_backups"`
	RotateLogMaxAge          int           `toml:"log_rotate_max_age"`
	RotateLogLocalTime       bool          `toml:"log_rotate_local_time"`
	RotateLogCompress        bool          `toml:"log_rotate_compress"`
	APISocket                string        `toml:"api_socket"`
	RecoverPolicy            string        `toml:"recover_policy"`
}

type SnapshotterConfig struct {
	StartupFlag command.Args `toml:"snapshotter"`
}

func LoadSnapshotterConfig(snapshotterConfigPath string) (*SnapshotterConfig, error) {
	var config SnapshotterConfig
	// get nydus-snapshotter configuration from specified path of toml file
	if snapshotterConfigPath == "" {
		return nil, errors.New("snapshotter configuration path cannot be empty")
	}
	tree, err := toml.LoadFile(snapshotterConfigPath)
	if err != nil {
		return nil, errors.Wrapf(err, "load snapshotter configuration file %q", snapshotterConfigPath)
	}
	if err = tree.Unmarshal(config); err != nil {
		return nil, errors.Wrapf(err, "unmarshal snapshotter configuration file %q", snapshotterConfigPath)
	}
	return &config, nil
}

func SetStartupParameter(startupFlag *command.Args, cfg *Config) error {
	if startupFlag == nil {
		return errors.New("no startup parameter provided")
	}
	var daemonCfg DaemonConfig
	if err := LoadConfig(startupFlag.ConfigPath, &daemonCfg); err != nil {
		return errors.Wrapf(err, "failed to load config file %q", startupFlag.ConfigPath)
	}
	cfg.DaemonCfg = daemonCfg

	if startupFlag.ValidateSignature {
		if startupFlag.PublicKeyFile == "" {
			return errors.New("need to specify publicKey file for signature validation")
		} else if _, err := os.Stat(startupFlag.PublicKeyFile); err != nil {
			return errors.Wrapf(err, "failed to find publicKey file %q", startupFlag.PublicKeyFile)
		}
	}
	cfg.PublicKeyFile = startupFlag.PublicKeyFile
	cfg.ValidateSignature = startupFlag.ValidateSignature
	if len(cfg.DaemonCfg.DomainID) != 0 {
		log.L.Warnf("Linux Kernel Shared Domain feature in use. make sure your kernel version >= 6.1")
	}

	daemonMode, err := parseDaemonMode(startupFlag.DaemonMode)
	if err != nil {
		return err
	}

	// Give --shared-daemon higher priority
	cfg.DaemonMode = daemonMode
	if startupFlag.SharedDaemon {
		cfg.DaemonMode = DaemonModeShared
	}

	if startupFlag.FsDriver == FsDriverFscache && daemonMode != DaemonModeShared {
		return errors.New("`fscache` driver only supports `shared` daemon mode")
	}

	cfg.RootDir = startupFlag.RootDir
	if len(cfg.RootDir) == 0 {
		return errors.New("invalid empty root directory")
	}

	if startupFlag.RootDir == defaultRootDir {
		if entries, err := os.ReadDir(oldDefaultRootDir); err == nil {
			if len(entries) != 0 {
				log.L.Warnf("Default root directory is changed to %s", defaultRootDir)
			}
		}
	}

	cfg.CacheDir = startupFlag.CacheDir
	if len(cfg.CacheDir) == 0 {
		cfg.CacheDir = filepath.Join(cfg.RootDir, "cache")
	}

	cfg.LogLevel = startupFlag.LogLevel
	// Always let options from CLI override those from configuration file.
	cfg.LogToStdout = startupFlag.LogToStdout
	cfg.LogDir = startupFlag.LogDir
	if len(cfg.LogDir) == 0 {
		cfg.LogDir = filepath.Join(cfg.RootDir, logging.DefaultLogDirName)
	}
	cfg.RotateLogMaxSize = defaultRotateLogMaxSize
	cfg.RotateLogMaxBackups = defaultRotateLogMaxBackups
	cfg.RotateLogMaxAge = defaultRotateLogMaxAge
	cfg.RotateLogLocalTime = defaultRotateLogLocalTime
	cfg.RotateLogCompress = defaultRotateLogCompress

	d, err := time.ParseDuration(startupFlag.GCPeriod)
	if err != nil {
		return errors.Wrapf(err, "parse gc period %v failed", startupFlag.GCPeriod)
	}
	cfg.GCPeriod = d

	cfg.Address = startupFlag.Address
	cfg.APISocket = startupFlag.APISocket
	cfg.CleanupOnClose = startupFlag.CleanupOnClose
	cfg.ConvertVpcRegistry = startupFlag.ConvertVpcRegistry
	cfg.DisableCacheManager = startupFlag.DisableCacheManager
	cfg.EnableMetrics = startupFlag.EnableMetrics
	cfg.EnableStargz = startupFlag.EnableStargz
	cfg.EnableNydusOverlayFS = startupFlag.EnableNydusOverlayFS
	cfg.FsDriver = startupFlag.FsDriver
	cfg.MetricsFile = startupFlag.MetricsFile
	cfg.NydusdBinaryPath = startupFlag.NydusdBinaryPath
	cfg.NydusImageBinaryPath = startupFlag.NydusImageBinaryPath
	cfg.NydusdThreadNum = startupFlag.NydusdThreadNum
	cfg.SyncRemove = startupFlag.SyncRemove
	cfg.KubeconfigPath = startupFlag.KubeconfigPath
	cfg.EnableKubeconfigKeychain = startupFlag.EnableKubeconfigKeychain
	cfg.RecoverPolicy = startupFlag.RecoverPolicy

	return cfg.SetupNydusBinaryPaths()
}

func (c *Config) FillupWithDefaults() error {
	if c.LogLevel == "" {
		c.LogLevel = DefaultLogLevel
	}
	if c.DaemonCfgPath == "" {
		c.DaemonCfgPath = defaultNydusDaemonConfigPath
	}

	if c.DaemonMode == "" {
		c.DaemonMode = DefaultDaemonMode
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
	var daemonCfg DaemonConfig
	if err := LoadConfig(c.DaemonCfgPath, &daemonCfg); err != nil {
		return errors.Wrapf(err, "failed to load config file %q", c.DaemonCfgPath)
	}
	c.DaemonCfg = daemonCfg
	return c.SetupNydusBinaryPaths()
}

func (c *Config) SetupNydusBinaryPaths() error {
	// when using DaemonMode = none, nydusd and nydus-image binaries are not required
	if c.DaemonMode == DaemonModeNone {
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
