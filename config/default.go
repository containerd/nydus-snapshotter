/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package config

import (
	"os/exec"
)

const (
	defaultDaemonMode string = string(DaemonModeMultiple)

	defaultFsDriver string = FsDriverFusedev

	DefaultLogLevel string = "info"
	defaultGCPeriod string = "24h"

	defaultNydusDaemonConfigPath string = "/etc/nydus/nydusd-config.json"
	nydusdBinaryName             string = "nydusd"
	nydusImageBinaryName         string = "nydus-image"

	defaultRootDir                 = "/var/lib/containerd-nydus"
	defaultSystemControllerAddress = "/var/run/containerd-nydus/system.sock"

	// Log rotation
	defaultRotateLogMaxSize    = 200 // 200 megabytes
	defaultRotateLogMaxBackups = 10
	defaultRotateLogMaxAge     = 0 // days
	defaultRotateLogLocalTime  = true
	defaultRotateLogCompress   = true
)

func (c *SnapshotterConfig) FillUpWithDefaults() error {
	c.Root = defaultRootDir

	// essential configuration
	if c.DaemonMode == "" {
		c.DaemonMode = defaultDaemonMode
	}

	// system controller configuration
	c.SystemControllerConfig.Address = defaultSystemControllerAddress

	// logging configuration
	logConfig := &c.LoggingConfig
	if logConfig.LogLevel == "" {
		logConfig.LogLevel = DefaultLogLevel
	}
	logConfig.RotateLogMaxSize = defaultRotateLogMaxSize
	logConfig.RotateLogMaxBackups = defaultRotateLogMaxBackups
	logConfig.RotateLogMaxAge = defaultRotateLogMaxAge
	logConfig.RotateLogLocalTime = defaultRotateLogLocalTime
	logConfig.RotateLogCompress = defaultRotateLogCompress

	// daemon configuration
	daemonConfig := &c.DaemonConfig
	if daemonConfig.NydusdConfigPath == "" {
		daemonConfig.NydusdConfigPath = defaultNydusDaemonConfigPath
	}
	daemonConfig.RecoverPolicy = RecoverPolicyRestart.String()
	daemonConfig.FsDriver = defaultFsDriver

	// cache configuration
	cacheConfig := &c.CacheManagerConfig
	if cacheConfig.GCPeriod == "" {
		cacheConfig.GCPeriod = defaultGCPeriod
	}

	return c.SetupNydusBinaryPaths()
}

func (c *SnapshotterConfig) SetupNydusBinaryPaths() error {
	// when using DaemonMode = none, nydusd and nydus-image binaries are not required
	if c.DaemonMode == string(DaemonModeNone) {
		return nil
	}

	// resolve nydusd path
	if path, err := exec.LookPath(nydusdBinaryName); err == nil {
		c.DaemonConfig.NydusdPath = path
	}

	// resolve nydus-image path
	if path, err := exec.LookPath(nydusImageBinaryName); err == nil {
		c.DaemonConfig.NydusImagePath = path
	}

	return nil
}
