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

	"github.com/containerd/nydus-snapshotter/cmd/containerd-nydus-grpc/pkg/logging"
	"github.com/pelletier/go-toml"
	"github.com/pkg/errors"
	exec "golang.org/x/sys/execabs"

	"github.com/containerd/nydus-snapshotter/cmd/containerd-nydus-grpc/pkg/command"
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
}

type SnapshotterConfig struct {
	StartupParameterCfg command.Args `toml:"StartupParameter"`
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
