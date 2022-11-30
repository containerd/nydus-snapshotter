/*
 * Copyright (c) 2022. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package command

import (
	"time"

	"github.com/sirupsen/logrus"
)

const (
	DefaultDaemonMode DaemonMode = DaemonModeMultiple
)

const (
	DefaultGCPeriod = 24 * time.Hour
)

const (
	DefaultLogLevel   = logrus.InfoLevel
	DefaultLogDirName = "logs"
)

const (
	FsDriverFusedev = "fusedev"
	FsDriverFscache = "fscache"
)

const (
	DefaultRootDir    = "/var/lib/containerd-nydus"
	DefaultOldRootDir = "/var/lib/containerd-nydus-grpc"
)

const (
	DefaultConfigPath       = "/etc/nydus-snapshotter/config.toml"
	DefaultDaemonConfigPath = "/etc/nydus/config.json"
)

const (
	defaultAddress   = "/run/containerd-nydus/containerd-nydus-grpc.sock"
	defaultAPISocket = "/var/lib/containerd-nydus/api.sock"
	defaultPublicKey = "/signing/nydus-image-signing-public.key"
)
