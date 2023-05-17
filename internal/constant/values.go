/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

// constants of nydus snapshotter CLI config

package constant

const (
	DaemonModeMultiple string = "multiple"
	DaemonModeShared   string = "shared"
	DaemonModeNone     string = "none"
	DaemonModeInvalid  string = ""
)

const (
	FsDriverFusedev string = "fusedev"
	FsDriverFscache string = "fscache"
)

const (
	DefaultDaemonMode string = DaemonModeMultiple

	DefaultFsDriver string = FsDriverFusedev

	DefaultLogLevel string = "info"
	DefaultGCPeriod string = "24h"

	DefaultNydusDaemonConfigPath string = "/etc/nydus/nydusd-config.json"
	NydusdBinaryName             string = "nydusd"
	NydusImageBinaryName         string = "nydus-image"

	DefaultRootDir                 = "/var/lib/containerd-nydus"
	DefaultAddress                 = "/run/containerd-nydus/containerd-nydus-grpc.sock"
	DefaultSystemControllerAddress = "/run/containerd-nydus/system.sock"

	// Log rotation
	DefaultRotateLogMaxSize    = 200 // 200 megabytes
	DefaultRotateLogMaxBackups = 10
	DefaultRotateLogMaxAge     = 0 // days
	DefaultRotateLogLocalTime  = true
	DefaultRotateLogCompress   = true
)
