/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package collector

import (
	"context"

	"github.com/containerd/nydus-snapshotter/pkg/metrics/data"

	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/tool"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
)

type Collector interface {
	// Collect metrics to prometheus data.
	Collect()
}

func NewDaemonEventCollector(event string) *DaemonEventCollector {
	return &DaemonEventCollector{event}
}

func NewFsMetricsCollector(m *types.FsMetrics, imageRef string) *FsMetricsCollector {
	return &FsMetricsCollector{m, imageRef}
}

func NewSnapshotterMetricsCollector(ctx context.Context, cacheDir string, pid int) (*SnapshotterMetricsCollector, error) {
	currentStat, err := tool.GetCurrentStat(pid)
	if err != nil {
		return nil, errors.Wrapf(err, "can not get current stat")
	}
	return &SnapshotterMetricsCollector{ctx, cacheDir, pid, currentStat}, nil
}

func NewSnapshotMetricsTimer(method SnapshotMethod) *prometheus.Timer {
	return CollectSnapshotMetricsTimer(data.SnapshotEventElapsedHists, method)
}
