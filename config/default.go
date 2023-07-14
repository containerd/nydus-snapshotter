/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package config

import (
	"os/exec"

	"github.com/containerd/nydus-snapshotter/internal/constant"
)

func (c *SnapshotterConfig) FillUpWithDefaults() error {
	c.Version = 1
	c.Root = constant.DefaultRootDir
	c.Address = constant.DefaultAddress

	// essential configuration
	if c.DaemonMode == "" {
		c.DaemonMode = constant.DefaultDaemonMode
	}

	// system controller configuration
	c.SystemControllerConfig.Address = constant.DefaultSystemControllerAddress

	// logging configuration
	logConfig := &c.LoggingConfig
	if logConfig.LogLevel == "" {
		logConfig.LogLevel = constant.DefaultLogLevel
	}
	logConfig.RotateLogMaxSize = constant.DefaultRotateLogMaxSize
	logConfig.RotateLogMaxBackups = constant.DefaultRotateLogMaxBackups
	logConfig.RotateLogMaxAge = constant.DefaultRotateLogMaxAge
	logConfig.RotateLogLocalTime = constant.DefaultRotateLogLocalTime
	logConfig.RotateLogCompress = constant.DefaultRotateLogCompress

	// daemon configuration
	daemonConfig := &c.DaemonConfig
	if daemonConfig.NydusdConfigPath == "" {
		daemonConfig.NydusdConfigPath = constant.DefaultNydusDaemonConfigPath
	}
	daemonConfig.RecoverPolicy = RecoverPolicyRestart.String()
	daemonConfig.FsDriver = constant.DefaultFsDriver
	daemonConfig.LogRotationSize = constant.DefaultDaemonRotateLogMaxSize

	// cache configuration
	cacheConfig := &c.CacheManagerConfig
	if cacheConfig.GCPeriod == "" {
		cacheConfig.GCPeriod = constant.DefaultGCPeriod
	}

	return c.SetupNydusBinaryPaths()
}

func (c *SnapshotterConfig) SetupNydusBinaryPaths() error {
	// resolve nydusd path
	if path, err := exec.LookPath(constant.NydusdBinaryName); err == nil {
		c.DaemonConfig.NydusdPath = path
	}

	// resolve nydus-image path
	if path, err := exec.LookPath(constant.NydusImageBinaryName); err == nil {
		c.DaemonConfig.NydusImagePath = path
	}

	return nil
}
