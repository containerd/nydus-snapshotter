/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package collector

import (
	"context"

	"github.com/containerd/continuity/fs"
	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/data"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/tool"
	"github.com/prometheus/client_golang/prometheus"
)

type SnapshotterMetricsCollector struct {
	ctx      context.Context
	cacheDir string
	pid      int
	lastStat *tool.Stat
}

type SnapshotMethod string

const (
	SnapshotMethodUnknown SnapshotMethod = "UNKNOWN"
	SnapshotMethodPrepare SnapshotMethod = "PREPARE"
	SnapshotMethodMount   SnapshotMethod = "MOUNTS"
	SnapshotMethodCleanup SnapshotMethod = "CLEANUP"
	SnapshotMethodRemove  SnapshotMethod = "REMOVE"
)

func (s *SnapshotterMetricsCollector) CollectCacheUsage() {
	du, err := fs.DiskUsage(s.ctx, s.cacheDir)
	if err != nil {
		log.L.Warnf("Get disk usage failed: %v", err)
	} else {
		data.CacheUsage.Set(float64(du.Size) / 1024)
	}
}

func (s *SnapshotterMetricsCollector) CollectResourceUsage() {
	currentStat, err := tool.GetProcessStat(s.pid)
	if err != nil {
		log.L.Warnf("Can not get current process stat.")
		return
	}

	if s.lastStat == nil {
		log.L.Debug("Can not get resource usage information: lastStat is nil")
		s.lastStat = currentStat
		return
	}

	cpuSys := (currentStat.Stime - s.lastStat.Stime) / tool.ClkTck
	cpuUsr := (currentStat.Utime - s.lastStat.Utime) / tool.ClkTck

	cpuPercent, err := tool.CalculateCPUUtilization(s.lastStat, currentStat)
	if err != nil {
		log.L.WithError(err).Warnf("Failed to calculate CPU utilization")
	}

	s.lastStat = currentStat

	memory := currentStat.Rss * tool.PageSize
	runTime := currentStat.Uptime - currentStat.Start/tool.ClkTck

	data.CPUSystem.Set(tool.FormatFloat64(cpuSys, 2))
	data.CPUUser.Set(tool.FormatFloat64(cpuUsr, 2))
	data.CPUUsage.Set(tool.FormatFloat64(cpuPercent, 2))
	data.MemoryUsage.Set(tool.FormatFloat64(memory/1024, 2))
	data.Fds.Set(currentStat.Fds)
	data.RunTime.Set(tool.FormatFloat64(runTime, 2))
	data.Thread.Set(currentStat.Thread)
}

func (s *SnapshotterMetricsCollector) Collect() {
	s.CollectCacheUsage()
	s.CollectResourceUsage()
}

func CollectSnapshotMetricsTimer(h *prometheus.HistogramVec, event SnapshotMethod) *prometheus.Timer {
	return prometheus.NewTimer(
		prometheus.ObserverFunc(
			(func(v float64) {
				h.WithLabelValues(string(event)).Observe(tool.FormatFloat64(v*1000, 6))
			})))
}
