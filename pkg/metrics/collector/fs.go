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
	mtypes "github.com/containerd/nydus-snapshotter/pkg/metrics/types"
)

type FsMetricsCollector struct {
	Metrics  *types.FsMetrics
	ImageRef string
}

type FsMetricsVecCollector struct {
	MetricsVec []FsMetricsCollector
}

func (f *FsMetricsCollector) Collect() {
	if f.Metrics == nil {
		log.L.Warnf("can not collect FS metrics: Metrics is nil")
		return
	}
	data.FsTotalRead.WithLabelValues(f.ImageRef).Set(float64(f.Metrics.DataRead))
	data.FsReadHit.WithLabelValues(f.ImageRef).Set(float64(f.Metrics.FopHits[mtypes.Read]))
	data.FsReadError.WithLabelValues(f.ImageRef).Set(float64(f.Metrics.FopErrors[mtypes.Read]))

	for _, h := range data.MetricHists {
		o, err := h.ToConstHistogram(f.Metrics, f.ImageRef)
		if err != nil {
			log.L.Warnf("failed to new const histogram for %s, error: %v", h.Desc.String(), err)
			return
		}
		h.Save(o)
	}
}

func (f *FsMetricsVecCollector) Clear() {
	for _, h := range data.MetricHists {
		h.Clear()
	}
}

func (f *FsMetricsVecCollector) Collect() {
	f.Clear()
	for _, fsMetrics := range f.MetricsVec {
		fsMetrics.Collect()
	}
}
