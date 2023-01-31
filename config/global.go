/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

// Expose configurations across nydus-snapshotter, the configurations is parsed
// and extracted from nydus-snapshotter toml based configuration file or command line

package config

import (
	"path/filepath"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
)

var (
	globalConfig GlobalConfig
)

// Retain the configurations that must be parsed and converted and the
// configurations that are not easy to access from some modules.
// Or avoid calculating repeatedly
type GlobalConfig struct {
	origin           *SnapshotterConfig
	SnapshotsDir     string
	DaemonMode       DaemonMode
	SocketRoot       string
	ConfigRoot       string
	DaemonThreadsNum int
	CacheGCPeriod    time.Duration
}

func GetDaemonMode() DaemonMode {
	return globalConfig.DaemonMode
}

func GetSnapshotsRootDir() string {
	return globalConfig.SnapshotsDir
}

func GetSocketRoot() string {
	return globalConfig.SocketRoot
}

func GetConfigRoot() string {
	return globalConfig.ConfigRoot

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

func GetDaemonThreadsNumber() int {
	return globalConfig.origin.DaemonConfig.ThreadsNumber
}

func GetLogToStdout() bool {
	return globalConfig.origin.LoggingConfig.LogToStdout
}

func ProcessConfigurations(c *SnapshotterConfig) error {
	globalConfig.origin = c

	globalConfig.SnapshotsDir = filepath.Join(c.Root, "snapshots")
	globalConfig.ConfigRoot = filepath.Join(c.Root, "config")
	globalConfig.SocketRoot = filepath.Join(c.Root, "socket")

	if c.CacheManagerConfig.GCPeriod != "" {
		d, err := time.ParseDuration(c.CacheManagerConfig.GCPeriod)
		if err != nil {
			return errors.Errorf("invalid GC period %s", c.CacheManagerConfig.GCPeriod)
		}
		globalConfig.CacheGCPeriod = d
	}

	m, err := parseDaemonMode(c.DaemonMode)
	if err != nil {
		return err
	}

	if c.DaemonConfig.FsDriver == FsDriverFscache && m != DaemonModeShared {
		log.L.Infof("fscache driver must enforce \"shared\" daemon mode, change it forcefully from %s", m)
		m = DaemonModeShared
	}

	globalConfig.DaemonMode = m

	return nil
}
