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
	defaultDurationBuckets = []float64{.5, 1, 5, 10, 50, 100, 150, 200, 250, 300, 350, 400, 600, 1000}
	snapshotEventLabel     = "snapshot_operation"
)

var (
	SnapshotEventElapsedHists = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "snapshotter_snapshot_operation_elapsed_milliseconds",
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
			Name: "snapshotter_cpu_usage_percentage",
			Help: "CPU usage percentage of snapshotter.",
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
			Help: "Running time of snapshotter from starting.",
		},
	)

	Thread = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "snapshotter_thread_counts",
			Help: "Thread counts of snapshotter.",
		},
	)
)
