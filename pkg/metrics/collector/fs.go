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

type FsMetricsCollector struct {
	Metrics  *types.FsMetrics
	ImageRef string
}

func (f *FsMetricsCollector) Collect() {
	if f.Metrics == nil {
		log.L.Warnf("can not collect FS metrics: Metrics is nil")
		return
	}
	data.ReadCount.WithLabelValues(f.ImageRef).Set(float64(f.Metrics.DataRead))
	data.OpenFdCount.WithLabelValues(f.ImageRef).Set(float64(f.Metrics.NrOpens))
	data.OpenFdMaxCount.WithLabelValues(f.ImageRef).Set(float64(f.Metrics.NrMaxOpens))
	data.LastFopTimestamp.WithLabelValues(f.ImageRef).Set(float64(f.Metrics.LastFopTp))

	for _, h := range data.MetricHists {
		o, err := h.ToConstHistogram(f.Metrics, f.ImageRef)
		if err != nil {
			log.L.Warnf("failed to new const histogram for %s, error: %v", h.Desc.String(), err)
			return
		}
		h.Save(o)
	}
}
