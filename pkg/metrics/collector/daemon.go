/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package collector

import (
	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/data"
)

type DaemonEventCollector struct {
	event string
}

type DaemonInfoCollector struct {
	Version *types.BuildTimeInfo
	value   float64
}

func (d *DaemonEventCollector) Collect() {
	data.NydusdEventCount.WithLabelValues(d.event).Inc()
}

func (d *DaemonInfoCollector) Collect() {
	if d.Version == nil {
		log.L.Warnf("failed to collect daemon count, version is invalid")
		return
	}
	data.NydusdCount.WithLabelValues(d.Version.PackageVer).Add(d.value)
}
