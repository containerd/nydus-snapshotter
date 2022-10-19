/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import "github.com/containerd/nydus-snapshotter/config"

func (m *Manager) isOneDaemon() bool {
	return m.daemonMode == config.DaemonModeShared || m.daemonMode == config.DaemonModePrefetch
}

func (m *Manager) isNoneDaemon() bool {
	return m.daemonMode == config.DaemonModeNone
}

func (m *Manager) IsSharedDaemon() bool {
	return m.daemonMode == config.DaemonModeShared
}

func (m *Manager) IsPrefetchDaemon() bool {
	return m.daemonMode == config.DaemonModePrefetch
}
