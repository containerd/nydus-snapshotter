/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package collector

import (
	"time"

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/data"
	mtypes "github.com/containerd/nydus-snapshotter/pkg/metrics/types"
)

var OPCodeMap = map[uint32]string{
	15: "OP_READ",
}

type FsMetricsCollector struct {
	Metrics  *types.FsMetrics
	ImageRef string
}

type FsMetricsVecCollector struct {
	MetricsVec []FsMetricsCollector
}

type InflightMetricsVecCollector struct {
	MetricsVec     []*types.InflightMetrics
	HungIOInterval time.Duration
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

func (i *InflightMetricsVecCollector) Collect() {
	if i.MetricsVec == nil {
		log.L.Warnf("can not collect inflight metrics: Metrics is nil")
		return
	}

	// The TimestampSecs of inflight IO is the beginning time of this request.
	// We can calculate the elapsed time by time.Now().Unix() - TimestampSecs.
	// The inflight IOs which have a longer elapsed time than the HungIOInterval (default 10 seconds) are hung IOs.
	totalHungIOMap := 0
	nowTime := time.Now()
	for _, daemonInflightIOMetrics := range i.MetricsVec {
		for _, inflightIOMetric := range daemonInflightIOMetrics.Values {
			elapsed := nowTime.Sub(time.Unix(int64(inflightIOMetric.TimestampSecs), 0))
			if elapsed >= i.HungIOInterval {
				totalHungIOMap++
				log.L.Debugf("Record hung IO, Inode: %v, Opcode: %v, Unique: %v, Elapsed: %v",
					inflightIOMetric.Inode, inflightIOMetric.Opcode, inflightIOMetric.Unique, elapsed)
			}
		}
	}
	data.TotalHungIO.Set(float64(totalHungIOMap))
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
