/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package collector

import (
	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/data"
)

type DaemonEventCollector struct {
	event types.DaemonState
}

type DaemonInfoCollector struct {
	Version *types.BuildTimeInfo
	value   float64
}

type DaemonResourceCollector struct {
	DaemonID string
	Value    float64
}

type DaemonImageCollector struct {
	DaemonID string
	ImageRef string
}

func (d *DaemonEventCollector) Collect() {
	data.NydusdEventCount.WithLabelValues(string(d.event)).Inc()
}

func (d *DaemonInfoCollector) Collect() {
	if d.Version == nil {
		log.L.Warnf("failed to collect daemon count, version is invalid")
		return
	}
	data.NydusdCount.WithLabelValues(d.Version.PackageVer).Add(d.value)
}

func (d *DaemonResourceCollector) Collect() {
	data.NydusdRSS.WithLabelValues(d.DaemonID).Set(d.Value)
}

func (d *DaemonImageCollector) Collect() {
	data.NydusdImageInfo.WithLabelValues(d.DaemonID, d.ImageRef).Set(1)
}

func (d *DaemonImageCollector) Delete() {
	data.NydusdImageInfo.DeleteLabelValues(d.DaemonID, d.ImageRef)
}
