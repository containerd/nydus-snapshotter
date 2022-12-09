/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package data

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	defaultDurationBuckets = []float64{.1, .15, .2, .3, .5, 1, 1.5, 2, 3, 5, 10, 25, 60}
	snapshotEventLabel     = "snapshot_event"
)

var (
	SnapshotEventElapsedHists = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "snapshotter_snapshot_event_elapsed_milliseconds",
			Help:    "The elapsed time for snapshot events.",
			Buckets: defaultDurationBuckets,
		},
		[]string{snapshotEventLabel},
	)

	CacheUsage = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "snapshotter_cache_usage_kilobytes",
			Help: "Disk usage of snapshotter local cache.",
		},
	)

	CPUUsage = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "snapshotter_cpu_usage_percent",
			Help: "Cpu usage percent of snapshotter.",
		},
	)

	MemoryUsage = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "snapshotter_memory_usage_kilobytes",
			Help: "Memory usage (RSS) of snapshotter.",
		},
	)

	CPUSystem = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "snapshotter_cpu_system_time_seconds",
			Help: "CPU time of snapshotter in system.",
		},
	)

	CPUUser = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "snapshotter_cpu_user_time_seconds",
			Help: "CPU time of snapshotter in user.",
		},
	)

	Fds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "snapshotter_fd_counts",
			Help: "Fd counts of snapshotter.",
		},
	)

	RunTime = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "snapshotter_run_time_seconds",
			Help: "Run time of snapshotter from starting.",
		},
	)

	Thread = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "snapshotter_thread_counts",
			Help: "Thread counts of snapshotter.",
		},
	)
)
