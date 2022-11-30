/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package config

import (
	"os/exec"
	"time"

	"github.com/containerd/nydus-snapshotter/internal/containerd-nydus-grpc/command"
)

type Config struct {
	Version        string         `toml:"version"`
	CleanupOnClose bool           `toml:"cleanup_on_close"`
	EnableStargz   bool           `toml:"enable_stargz"`
	RootDir        string         `toml:"root_dir"`
	Binaries       BinariesConfig `toml:"binaries"`
	Log            LogConfig      `toml:"log"`
	Metrics        MetricsConfig  `toml:"metrics"`
	System         SystemConfig   `toml:"system"`
	Remote         RemoteConfig   `toml:"remote"`
	Snapshot       SnapshotConfig `toml:"snapshot"`
	Cache          CacheConfig    `toml:"cache"`
	Image          ImageConfig    `toml:"image"`
	Daemon         DaemonConfig   `toml:"daemon"`
}

type BinariesConfig struct {
	NydusdBinaryPath     string `toml:"nydusd_binary_path"`
	NydusImageBinaryPath string `toml:"nydus_image_binary_path"`
}

type LogConfig struct {
	Dir                 string `toml:"dir"`
	Level               string `toml:"level"`
	Stdout              bool   `toml:"stdout"`
	RotateLogCompress   bool   `toml:"rotate_compress"`
	RotateLogLocalTime  bool   `toml:"rotate_local_time"`
	RotateLogMaxAge     int    `toml:"rotate_max_age"`
	RotateLogMaxBackups int    `toml:"rotate_max_backups"`
	RotateLogMaxSize    int    `toml:"rotate_max_size"`
}

type MetricsConfig struct {
	Enable bool `toml:"enable"`
}

type SystemConfig struct {
	SocketPath string `toml:"socket_path"`
}

type RemoteConfig struct {
	Auth AuthConfig `toml:"auth"`
}

type AuthConfig struct {
	EnableKubeconfigKeychain bool   `toml:"enable_kubeconfig_keychain"`
	KubeconfigPath           string `toml:"kubeconfig_path"`
}

type SnapshotConfig struct {
	EnableNydusOverlayFS bool `toml:"enable_nydus_overlayfs"`
	SyncRemove           bool `toml:"sync_remove"`
}

type CacheConfig struct {
	Enable   bool          `toml:"enable"`
	GCPeriod time.Duration `toml:"gc_period"`
}

type ImageConfig struct {
	PublicKeyFile     string `toml:"public_key_file"`
	ValidateSignature bool   `toml:"validate_signature"`
}

type DaemonConfig struct {
	FsDriver      string              `toml:"fs_drvier"`
	RecoverPolicy string              `toml:"recover_policy"`
	Log           DaemonLogConfig     `toml:"log"`
	Storage       DaemonStorageConfig `toml:"storage"`
	Fuse          FuseConfig          `toml:"fuse"`
	Fscache       FscacheConfig       `toml:"fscache"`
}

type DaemonLogConfig struct {
	Level string `toml:"level"`
}

type DaemonStorageConfig struct {
	DisableIndexedMap bool           `toml:"disable_indexed_map"`
	EnableCache       bool           `toml:"enable_cache"`
	RetryLimit        int            `toml:"retry_limit"`
	Scheme            string         `toml:"scheme"`
	Timeout           time.Duration  `toml:"timeout"`
	Type              string         `toml:"type"`
	ConnectTimeout    time.Duration  `toml:"connect_timeout"`
	Mirrors           MirrorsConfig  `toml:"mirrors"`
	Prefetch          PrefetchConfig `toml:"prefetch"`
}

type MirrorsConfig struct {
	Host                string            `toml:"host"`
	Headers             map[string]string `toml:"headers"`
	AuthThrough         bool              `toml:"auth_though"`
	HealthCheckInterval int               `toml:"health_check_interval"`
	FailureLimit        uint8             `toml:"failure_limit"`
	PingURL             string            `toml:"ping_url"`
}

type PrefetchConfig struct {
	Enable       bool `toml:"enable"`
	ThreadsCount int  `toml:"threads_count"`
	MergingSize  int  `toml:"merging_size"`
}

type FuseConfig struct {
	DigestValidate bool   `toml:"digest_validate"`
	EnableXattr    bool   `toml:"enable_xattr"`
	IostatsFiles   bool   `toml:"iostats_files"`
	Mode           string `toml:"mode"`
}

type FscacheConfig struct {
	Config FscacheSubConfig `toml:"config"`
}

type FscacheSubConfig struct {
	CacheType string `toml:"cache_type"`
	Type      string `toml:"type"`
}

// SetupNydusBinaryPaths setups binary paths of nydus.
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
