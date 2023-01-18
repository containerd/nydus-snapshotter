/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package config

import (
	"os/exec"
	"path/filepath"

	"github.com/containerd/nydus-snapshotter/cmd/containerd-nydus-grpc/pkg/logging"
)

const (
	DefaultDaemonMode string = string(DaemonModeMultiple)

	DefaultLogLevel string = "info"
	defaultGCPeriod string = "24h"

	defaultNydusDaemonConfigPath string = "/etc/nydus/nydusd-config.json"
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

func (c *SnapshotterConfig) FillUpWithDefaults() error {
	c.Root = defaultRootDir

	// essential configuration
	if c.DaemonMode == "" {
		c.DaemonMode = DefaultDaemonMode
	}

	// logging configuration
	logConfig := &c.LoggingConfig
	if logConfig.LogLevel == "" {
		logConfig.LogLevel = DefaultLogLevel
	}
	if len(logConfig.LogDir) == 0 {
		logConfig.LogDir = filepath.Join(c.Root, logging.DefaultLogDirName)
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

	// cache configuration
	cacheConfig := &c.CacheManagerConfig
	if cacheConfig.GCPeriod == "" {
		cacheConfig.GCPeriod = defaultGCPeriod
	}
	if len(cacheConfig.CacheDir) == 0 {
		cacheConfig.CacheDir = filepath.Join(c.Root, "cache")
	}

	return c.SetupNydusBinaryPaths()
}

func (c *SnapshotterConfig) SetupNydusBinaryPaths() error {
	// when using DaemonMode = none, nydusd and nydus-image binaries are not required
	if c.DaemonMode == string(DaemonModeNone) {
		return nil
	}

	// resolve nydusd path
	path, err := exec.LookPath(nydusdBinaryName)
	if err != nil {
		return err
	}
	c.DaemonConfig.NydusdPath = path

	// resolve nydus-image path
	path, err = exec.LookPath(nydusImageBinaryName)
	if err != nil {
		return err
	}
	c.DaemonConfig.NydusImagePath = path

	return nil
}
