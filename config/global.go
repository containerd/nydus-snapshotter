/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

// Expose configurations across nydus-snapshotter, the configurations is parsed
// and extracted from nydus-snapshotter toml based configuration file or command line

package config

import (
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/internal/logging"
	"github.com/containerd/nydus-snapshotter/pkg/utils/mount"
)

var (
	globalConfig GlobalConfig
)

// Global cached configuration information to help:
// - access configuration information without passing a configuration object
// - avoid frequent generation of information from configuration information
type GlobalConfig struct {
	origin           *SnapshotterConfig
	SnapshotsDir     string
	DaemonMode       DaemonMode
	SocketRoot       string
	ConfigRoot       string
	RootMountpoint   string
	DaemonThreadsNum int
	CacheGCPeriod    time.Duration
	MirrorsConfig    MirrorsConfig
}

func IsFusedevSharedModeEnabled() bool {
	return globalConfig.DaemonMode == DaemonModeShared
}

func GetDaemonMode() DaemonMode {
	return globalConfig.DaemonMode
}

func GetSnapshotsRootDir() string {
	return globalConfig.SnapshotsDir
}

func GetRootMountpoint() string {
	return globalConfig.RootMountpoint
}

func GetSocketRoot() string {
	return globalConfig.SocketRoot
}

func GetConfigRoot() string {
	return globalConfig.ConfigRoot
}

func GetMirrorsConfigDir() string {
	return globalConfig.MirrorsConfig.Dir
}

func GetFsDriver() string {
	return globalConfig.origin.DaemonConfig.FsDriver
}

func GetCacheGCPeriod() time.Duration {
	return globalConfig.CacheGCPeriod
}

func GetLogDir() string {
	return globalConfig.origin.LoggingConfig.LogDir
}

func GetLogLevel() string {
	return globalConfig.origin.LoggingConfig.LogLevel
}

func GetDaemonLogRotationSize() int {
	return globalConfig.origin.DaemonConfig.LogRotationSize
}

func GetDaemonThreadsNumber() int {
	return globalConfig.origin.DaemonConfig.ThreadsNumber
}

func GetLogToStdout() bool {
	return globalConfig.origin.LoggingConfig.LogToStdout
}

func IsSystemControllerEnabled() bool {
	return globalConfig.origin.SystemControllerConfig.Enable
}

func SystemControllerAddress() string {
	return globalConfig.origin.SystemControllerConfig.Address
}

func SystemControllerPprofAddress() string {
	return globalConfig.origin.SystemControllerConfig.DebugConfig.PprofAddress
}

func GetDaemonProfileCPUDuration() int64 {
	return globalConfig.origin.SystemControllerConfig.DebugConfig.ProfileDuration
}

const (
	TarfsLayerVerityOnly      string = "layer_verity_only"
	TarfsImageVerityOnly      string = "image_verity_only"
	TarfsLayerBlockDevice     string = "layer_block"
	TarfsImageBlockDevice     string = "image_block"
	TarfsLayerBlockWithVerity string = "layer_block_with_verity"
	TarfsImageBlockWithVerity string = "image_block_with_verity"
)

func GetTarfsMountOnHost() bool {
	return globalConfig.origin.Experimental.TarfsConfig.MountTarfsOnHost
}

func GetTarfsExportEnabled() bool {
	switch globalConfig.origin.Experimental.TarfsConfig.ExportMode {
	case TarfsLayerVerityOnly, TarfsLayerBlockDevice, TarfsLayerBlockWithVerity:
		return true
	case TarfsImageVerityOnly, TarfsImageBlockDevice, TarfsImageBlockWithVerity:
		return true
	default:
		return false
	}
}

// Returns (wholeImage, generateBlockImage, withVerityInfo)
// wholeImage: generate tarfs for the whole image instead of of a specific layer.
// generateBlockImage: generate a block image file.
// withVerityInfo: generate disk verity information.
func GetTarfsExportFlags() (bool, bool, bool) {
	switch globalConfig.origin.Experimental.TarfsConfig.ExportMode {
	case "layer_verity_only":
		return false, false, true
	case "image_verity_only":
		return true, false, true
	case "layer_block":
		return false, true, false
	case "image_block":
		return true, true, false
	case "layer_block_with_verity":
		return false, true, true
	case "image_block_with_verity":
		return true, true, true
	default:
		return false, false, false
	}
}

func ProcessConfigurations(c *SnapshotterConfig) error {
	if c.LoggingConfig.LogDir == "" {
		c.LoggingConfig.LogDir = filepath.Join(c.Root, logging.DefaultLogDirName)
	}
	if c.CacheManagerConfig.CacheDir == "" {
		c.CacheManagerConfig.CacheDir = filepath.Join(c.Root, "cache")
	}

	globalConfig.origin = c

	globalConfig.SnapshotsDir = filepath.Join(c.Root, "snapshots")
	globalConfig.ConfigRoot = filepath.Join(c.Root, "config")
	globalConfig.SocketRoot = filepath.Join(c.Root, "socket")
	globalConfig.RootMountpoint = filepath.Join(c.Root, "mnt")

	globalConfig.MirrorsConfig = c.RemoteConfig.MirrorsConfig

	if c.CacheManagerConfig.GCPeriod != "" {
		d, err := time.ParseDuration(c.CacheManagerConfig.GCPeriod)
		if err != nil {
			return errors.Errorf("invalid GC period '%s'", c.CacheManagerConfig.GCPeriod)
		}
		globalConfig.CacheGCPeriod = d
	}

	m, err := parseDaemonMode(c.DaemonMode)
	if err != nil {
		return err
	}

	if c.DaemonConfig.FsDriver == FsDriverFscache && m != DaemonModeShared {
		log.L.Infof("fscache driver only supports 'shared' mode, override daemon mode from '%s' to 'shared'", m)
		m = DaemonModeShared
	}

	globalConfig.DaemonMode = m

	return nil
}

func SetUpEnvironment(c *SnapshotterConfig) error {
	if err := os.MkdirAll(c.Root, 0700); err != nil {
		return errors.Wrapf(err, "create root dir %s", c.Root)
	}

	realPath, err := mount.NormalizePath(c.Root)
	if err != nil {
		return errors.Wrapf(err, "invalid root path")
	}
	c.Root = realPath
	return nil
}
