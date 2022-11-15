/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import "github.com/containerd/nydus-snapshotter/config"

func (m *Manager) IsSharedDaemon() bool {
	return m.daemonMode == config.DaemonModeShared
}
