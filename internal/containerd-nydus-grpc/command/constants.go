/*
 * Copyright (c) 2022. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package command

import "github.com/sirupsen/logrus"

const (
	DefaultDaemonMode DaemonMode = DaemonModeMultiple
	FsDriverFusedev   string     = "fusedev"
)

const (
	defaultAddress   = "/run/containerd-nydus/containerd-nydus-grpc.sock"
	defaultLogLevel  = logrus.InfoLevel
	defaultRootDir   = "/var/lib/containerd-nydus"
	defaultAPISocket = "/var/lib/containerd-nydus/api.sock"
	defaultGCPeriod  = "24h"
	defaultPublicKey = "/signing/nydus-image-signing-public.key"
)
