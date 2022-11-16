/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package config

import (
	"time"

	"github.com/containerd/nydus-snapshotter/internal/containerd-nydus-grpc/command"
)

type Config struct {
	Address                  string             `toml:"-"`
	ConvertVpcRegistry       bool               `toml:"-"`
	DaemonCfgPath            string             `toml:"daemon_cfg_path"`
	PublicKeyFile            string             `toml:"-"`
	RootDir                  string             `toml:"-"`
	CacheDir                 string             `toml:"cache_dir"`
	GCPeriod                 time.Duration      `toml:"gc_period"`
	ValidateSignature        bool               `toml:"validate_signature"`
	NydusdBinaryPath         string             `toml:"nydusd_binary_path"`
	NydusImageBinaryPath     string             `toml:"nydus_image_binary"`
	DaemonMode               command.DaemonMode `toml:"daemon_mode"`
	FsDriver                 string             `toml:"daemon_backend"`
	SyncRemove               bool               `toml:"sync_remove"`
	EnableMetrics            bool               `toml:"enable_metrics"`
	MetricsFile              string             `toml:"metrics_file"`
	EnableStargz             bool               `toml:"enable_stargz"`
	LogLevel                 string             `toml:"-"`
	LogDir                   string             `toml:"log_dir"`
	LogToStdout              bool               `toml:"log_to_stdout"`
	DisableCacheManager      bool               `toml:"disable_cache_manager"`
	EnableNydusOverlayFS     bool               `toml:"enable_nydus_overlayfs"`
	NydusdThreadNum          int                `toml:"nydusd_thread_num"`
	CleanupOnClose           bool               `toml:"cleanup_on_close"`
	KubeconfigPath           string             `toml:"kubeconfig_path"`
	EnableKubeconfigKeychain bool               `toml:"enable_kubeconfig_keychain"`
	RotateLogMaxSize         int                `toml:"log_rotate_max_size"`
	RotateLogMaxBackups      int                `toml:"log_rotate_max_backups"`
	RotateLogMaxAge          int                `toml:"log_rotate_max_age"`
	RotateLogLocalTime       bool               `toml:"log_rotate_local_time"`
	RotateLogCompress        bool               `toml:"log_rotate_compress"`
	APISocket                string             `toml:"api_socket"`
	RecoverPolicy            string             `toml:"recover_policy"`
}
