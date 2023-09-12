/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

// constants of nydus snapshotter CLI config

package constant

const (
	DaemonModeMultiple  string = "multiple"
	DaemonModeDedicated string = "dedicated"
	DaemonModeShared    string = "shared"
	DaemonModeNone      string = "none"
	DaemonModeInvalid   string = ""
)

const (
	// Mount RAFS filesystem by using EROFS over block devices.
	FsDriverBlockdev string = "blockdev"
	// Mount RAFS filesystem by using FUSE subsystem
	FsDriverFusedev string = "fusedev"
	// Mount RAFS filesystem by using fscache/EROFS.
	FsDriverFscache string = "fscache"
	// Only prepare/supply meta/data blobs, do not mount RAFS filesystem.
	FsDriverNodev string = "nodev"
	// Relay layer content download operation to other agents.
	FsDriverProxy string = "proxy"
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
	DefaultDaemonRotateLogMaxSize = 100 // 100 megabytes
	DefaultRotateLogMaxSize       = 200 // 200 megabytes
	DefaultRotateLogMaxBackups    = 5
	DefaultRotateLogMaxAge        = 0 // days
	DefaultRotateLogLocalTime     = true
	DefaultRotateLogCompress      = true
)
