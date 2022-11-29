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
	// Counters
	ReadCount = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_read_count",
			Help: "Total number read of a nydus fs, in Byte.",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)

	OpenFdCount = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_open_fd_count",
			Help: "Number of current open files.",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)

	OpenFdMaxCount = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_open_fd_max_count",
			Help: "Number of max open files.",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)

	LastFopTimestamp = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_last_fop_timestamp",
			Help: "Timestamp of last file operation.",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)
)

// Fs metric histograms
var MetricHists = []*mtypes.MetricHistogram{
	{
		Desc: prometheus.NewDesc(
			"nydusd_block_count_read_hist",
			"Read size histogram, in 1KB, 4KB, 16KB, 64KB, 128KB, 512K, 1024K.",
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
			"nydusd_fop_hit_hist",
			"File operations histogram",
			[]string{imageRefLabel},
			prometheus.Labels{},
		),
		Buckets: mtypes.MakeFopBuckets(),
		GetCounters: func(m *types.FsMetrics) []uint64 {
			return m.FopHits
		},
	},

	{
		Desc: prometheus.NewDesc(
			"nydusd_fop_errors_hist",
			"File operations' error histogram",
			[]string{imageRefLabel},
			prometheus.Labels{},
		),
		Buckets: mtypes.MakeFopBuckets(),
		GetCounters: func(m *types.FsMetrics) []uint64 {
			return m.FopErrors
		},
	},

	{
		Desc: prometheus.NewDesc(
			"nydusd_read_latency_hist",
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
