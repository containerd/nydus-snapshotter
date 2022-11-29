/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package collector

import (
	"time"

	"github.com/containerd/nydus-snapshotter/pkg/metrics/data"
)

type DaemonEventCollector struct{}

var GlobalDaemonEventCollector *DaemonEventCollector

func init() {
	GlobalDaemonEventCollector = &DaemonEventCollector{}
}

func (d *DaemonEventCollector) Collect(daemonID string, event string) {
	data.NydusdEvent.WithLabelValues(daemonID, time.Now().Format("2006-01-02 15:04:05.000"), event).Inc()
}
