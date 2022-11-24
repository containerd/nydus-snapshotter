/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package collector

import (
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/data"
	"github.com/pkg/errors"
)

type FsMetricsCollector struct{}

var GlobalFsMetricsCollector *FsMetricsCollector

func init() {
	GlobalFsMetricsCollector = &FsMetricsCollector{}
}

func (f *FsMetricsCollector) Collect(m *types.FsMetrics, imageRef string) error {
	data.ReadCount.WithLabelValues(imageRef).Set(float64(m.DataRead))
	data.OpenFdCount.WithLabelValues(imageRef).Set(float64(m.NrOpens))
	data.OpenFdMaxCount.WithLabelValues(imageRef).Set(float64(m.NrMaxOpens))
	data.LastFopTimestamp.WithLabelValues(imageRef).Set(float64(m.LastFopTp))

	for _, h := range data.MetricHists {
		o, err := h.ToConstHistogram(m, imageRef)
		if err != nil {
			return errors.Wrapf(err, "failed to new const histogram for %s", h.Desc.String())
		}
		h.Save(o)
	}

	return nil
}
