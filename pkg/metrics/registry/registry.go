/*
 * Copyright (c) 2021. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package registry

import (
	"github.com/containerd/nydus-snapshotter/pkg/metrics/data"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	Registry = prometheus.NewRegistry()
)

func init() {
	Registry.MustRegister(
		data.ReadCount,
		data.OpenFdCount,
		data.OpenFdMaxCount,
		data.LastFopTimestamp,
		data.NydusdEventCount,
		data.SnapshotEventElapsedHists,
		data.CacheUsage,
		data.CPUUsage,
		data.MemoryUsage,
		data.CPUSystem,
		data.CPUUser,
		data.Fds,
		data.RunTime,
		data.Thread,
	)

	for _, m := range data.MetricHists {
		Registry.MustRegister(m)
	}
}
