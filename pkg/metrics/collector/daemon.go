/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package collector

import (
	"github.com/containerd/nydus-snapshotter/pkg/metrics/data"
)

type DaemonEventCollector struct {
	event string
}

func (d *DaemonEventCollector) Collect() {
	data.NydusdEventCount.WithLabelValues(d.event).Inc()
}
