/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemon

import "github.com/containerd/nydus-snapshotter/config"

func (d *Daemon) IsMultipleDaemon() bool {
	return d.DaemonMode == config.DaemonModeMultiple
}

func (d *Daemon) IsSharedDaemon() bool {
	return d.DaemonMode == config.DaemonModeShared
}

func (d *Daemon) IsPrefetchDaemon() bool {
	return d.DaemonMode == config.DaemonModePrefetch
}
