/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package config

import "time"

const (
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
