/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import "github.com/containerd/nydus-snapshotter/internal/containerd-nydus-grpc/command"

func (m *Manager) IsSharedDaemon() bool {
	return m.daemonMode == command.DaemonModeShared
}
