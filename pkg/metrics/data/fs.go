/*
 * Copyright (c) 2021. Alibaba Cloud. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package data

import (
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	mtypes "github.com/containerd/nydus-snapshotter/pkg/metrics/types"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/types/ttl"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	imageRefLabel = "image_ref"
)

var (
	FsTotalRead = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_total_read_bytes",
			Help: "Total bytes read against the nydus filesystem",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)

	FsReadHit = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_read_hits",
			Help: "Total number of successful read operations.",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)
	FsReadError = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_read_errors",
			Help: "Total number of failed read operations.",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)
	TotalHungIO = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "nydusd_hung_io_counts",
			Help: "Total number of hung IOs.",
		},
	)
)

// Fs metric histograms
var MetricHists = []*mtypes.MetricHistogram{
	{
		Desc: prometheus.NewDesc(
			"nydusd_cumulative_read_block_bytes",
			"Cumulative read size histogram for different block size, in bytes.",
			[]string{imageRefLabel},
			prometheus.Labels{},
		),
		Buckets: []uint64{1, 4, 16, 64, 128, 512, 1024, 2048},
		GetCounters: func(m *types.FsMetrics) []uint64 {
			return m.BlockCountRead
		},
	},
	{
		Desc: prometheus.NewDesc(
			"nydusd_read_latency_microseconds",
			"Read latency histogram, in microseconds",
			[]string{imageRefLabel},
			prometheus.Labels{},
		),
		Buckets: []uint64{1, 20, 50, 100, 500, 1000, 2000, 4000},
		GetCounters: func(m *types.FsMetrics) []uint64 {
			return m.ReadLatencyDist
		},
	},
}
