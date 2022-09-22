/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package config

import (
	"os"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/cmd/containerd-nydus-grpc/pkg/logging"
	"github.com/pkg/errors"
	exec "golang.org/x/sys/execabs"
)

const (
	DefaultDaemonMode  string = "multiple"
	DaemonModeMultiple string = "multiple"
	DaemonModeShared   string = "shared"
	DaemonModeNone     string = "none"
	DaemonModePrefetch string = "prefetch"
	DefaultLogLevel    string = "info"
	defaultGCPeriod           = 24 * time.Hour

	defaultNydusDaemonConfigPath string = "/etc/nydus/config.json"
	nydusdBinaryName             string = "nydusd"
	nydusImageBinaryName         string = "nydus-image"

	defaultRootDir             = "/var/lib/containerd-nydus"
	oldDefaultRootDir          = "/var/lib/containerd-nydus-grpc"
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
	DaemonMode               string        `toml:"daemon_mode"`
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
	RecoverPolicy            string        `toml:"recover_policy"`
}

type StartupParameterConfig struct {
	Address                  string `toml:"address"`
	LogLevel                 string `toml:"log_level"`
	LogDir                   string `toml:"log_dir"`
	ConfigPath               string `toml:"config_path"`
	RootDir                  string `toml:"root_dir"`
	CacheDir                 string `toml:"cache_dir"`
	GCPeriod                 string `toml:"gc_period"`
	ValidateSignature        bool   `toml:"validate_signature"`
	PublicKeyFile            string `toml:"public_key_file"`
	ConvertVpcRegistry       bool   `toml:"convert_vpc_registry"`
	NydusdBinaryPath         string `toml:"nydusd_binary_path"`
	NydusImageBinaryPath     string `toml:"nydus_image_binary_path"`
	SharedDaemon             bool   `toml:"shared_daemon"`
	DaemonMode               string `toml:"daemon_mode"`
	FsDriver                 string `toml:"fs_driver"`
	SyncRemove               bool   `toml:"sync_remove"`
	EnableMetrics            bool   `toml:"enable_metrics"`
	MetricsFile              string `toml:"metrics_file"`
	EnableStargz             bool   `toml:"enable_stargz"`
	DisableCacheManager      bool   `toml:"disable_cache_manager"`
	LogToStdout              bool   `toml:"log_to_stdout"`
	EnableNydusOverlayFS     bool   `toml:"enable_nydus_overlay_fs"`
	NydusdThreadNum          int    `toml:"nydusd_thread_num"`
	CleanupOnClose           bool   `toml:"cleanup_on_close"`
	KubeconfigPath           string `toml:"kubeconfig_path"`
	EnableKubeconfigKeychain bool   `toml:"enable_kubeconfig_keychain"`
	RecoverPolicy            string `toml:"recover_policy"`
}

type SnapshotterConfig struct {
	StartupParameterCfg StartupParameterConfig `toml:"StartupParameter"`
}

func LoadShotterConfigFile(snapshotterConfigPath string, config *SnapshotterConfig) error {
	// get nydus-snapshotter configuration from specified path of toml file
	if snapshotterConfigPath == "" {
		return errors.New("snapshotter config path cannot be specified")
	}
	tree, err := toml.LoadFile(snapshotterConfigPath)
	if err != nil && !(os.IsNotExist(err)) {
		return errors.Wrapf(err, "failed to load snapshotter config file %q", snapshotterConfigPath)
	}
	if err := tree.Unmarshal(config); err != nil {
		return errors.Wrapf(err, "failed to unmarshal snapshotter config file %q", snapshotterConfigPath)
	}
	return nil
}

func SetStartupParameter(startupParameterCfg *StartupParameterConfig, cfg *Config) error {
	var daemonCfg DaemonConfig
	if err := LoadConfig(startupParameterCfg.ConfigPath, &daemonCfg); err != nil {
		return errors.Wrapf(err, "failed to load config file %q", startupParameterCfg.ConfigPath)
	}
	cfg.DaemonCfg = daemonCfg

	if startupParameterCfg.ValidateSignature {
		if startupParameterCfg.PublicKeyFile == "" {
			return errors.New("need to specify publicKey file for signature validation")
		} else if _, err := os.Stat(startupParameterCfg.PublicKeyFile); err != nil {
			return errors.Wrapf(err, "failed to find publicKey file %q", startupParameterCfg.PublicKeyFile)
		}
	}
	cfg.PublicKeyFile = startupParameterCfg.PublicKeyFile
	cfg.ValidateSignature = startupParameterCfg.ValidateSignature

	// Give --shared-daemon higher priority
	cfg.DaemonMode = startupParameterCfg.DaemonMode
	if startupParameterCfg.SharedDaemon {
		cfg.DaemonMode = DaemonModeShared
	}
	if startupParameterCfg.FsDriver == FsDriverFscache && startupParameterCfg.DaemonMode != DaemonModeShared {
		return errors.New("`fscache` driver only supports `shared` daemon mode")
	}

	cfg.RootDir = startupParameterCfg.RootDir
	if len(cfg.RootDir) == 0 {
		return errors.New("invalid empty root directory")
	}

	if startupParameterCfg.RootDir == defaultRootDir {
		if entries, err := os.ReadDir(oldDefaultRootDir); err == nil {
			if len(entries) != 0 {
				log.L.Warnf("Default root directory is changed to %s", defaultRootDir)
			}
		}
	}

	cfg.CacheDir = startupParameterCfg.CacheDir
	if len(cfg.CacheDir) == 0 {
		cfg.CacheDir = filepath.Join(cfg.RootDir, "cache")
	}

	cfg.LogLevel = startupParameterCfg.LogLevel
	// Always let options from CLI override those from configuration file.
	cfg.LogToStdout = startupParameterCfg.LogToStdout
	cfg.LogDir = startupParameterCfg.LogDir
	if len(cfg.LogDir) == 0 {
		cfg.LogDir = filepath.Join(cfg.RootDir, logging.DefaultLogDirName)
	}
	cfg.RotateLogMaxSize = defaultRotateLogMaxSize
	cfg.RotateLogMaxBackups = defaultRotateLogMaxBackups
	cfg.RotateLogMaxAge = defaultRotateLogMaxAge
	cfg.RotateLogLocalTime = defaultRotateLogLocalTime
	cfg.RotateLogCompress = defaultRotateLogCompress

	d, err := time.ParseDuration(startupParameterCfg.GCPeriod)
	if err != nil {
		return errors.Wrapf(err, "parse gc period %v failed", startupParameterCfg.GCPeriod)
	}
	cfg.GCPeriod = d

	cfg.Address = startupParameterCfg.Address
	cfg.CleanupOnClose = startupParameterCfg.CleanupOnClose
	cfg.ConvertVpcRegistry = startupParameterCfg.ConvertVpcRegistry
	cfg.DisableCacheManager = startupParameterCfg.DisableCacheManager
	cfg.EnableMetrics = startupParameterCfg.EnableMetrics
	cfg.EnableStargz = startupParameterCfg.EnableStargz
	cfg.EnableNydusOverlayFS = startupParameterCfg.EnableNydusOverlayFS
	cfg.FsDriver = startupParameterCfg.FsDriver
	cfg.MetricsFile = startupParameterCfg.MetricsFile
	cfg.NydusdBinaryPath = startupParameterCfg.NydusdBinaryPath
	cfg.NydusImageBinaryPath = startupParameterCfg.NydusImageBinaryPath
	cfg.NydusdThreadNum = startupParameterCfg.NydusdThreadNum
	cfg.SyncRemove = startupParameterCfg.SyncRemove
	cfg.KubeconfigPath = startupParameterCfg.KubeconfigPath
	cfg.EnableKubeconfigKeychain = startupParameterCfg.EnableKubeconfigKeychain

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
