/*
 * Copyright (c) 2025. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package collector

import (
	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/data"
)

type CacheMetricsCollector struct {
	Metrics  *types.CacheMetrics
	ImageRef string
	DaemonID string
}

type CacheMetricsVecCollector struct {
	MetricsVec []CacheMetricsCollector
}

func (c *CacheMetricsCollector) Collect() {
	if c.Metrics == nil {
		log.L.Warnf("can not collect cache metrics: Metrics is nil")
		return
	}

	prefetchTotalDuration := (c.Metrics.PrefetchEndTimeSecs*1000 +
		c.Metrics.PrefetchCumulativeTimeMillis) -
		(c.Metrics.PrefetchBeginTimeSecs*1000 +
			c.Metrics.PrefetchCumulativeTimeMillis)

	data.CachePartialHits.WithLabelValues(c.ImageRef).Set(float64(c.Metrics.PartialHits))
	data.CacheWholeHits.WithLabelValues(c.ImageRef).Set(float64(c.Metrics.WholeHits))
	data.CacheTotalRequests.WithLabelValues(c.ImageRef).Set(float64(c.Metrics.Total))
	data.CacheEntriesCount.WithLabelValues(c.ImageRef).Set(float64(c.Metrics.EntriesCount))
	data.CachePrefetchDataBytes.WithLabelValues(c.ImageRef).Set(float64(c.Metrics.PrefetchDataAmount))
	data.CachePrefetchRequestsCount.WithLabelValues(c.ImageRef).Set(float64(c.Metrics.PrefetchRequestsCount))
	data.CachePrefetchWorkers.WithLabelValues(c.ImageRef).Set(float64(c.Metrics.PrefetchWorkers))
	data.CachePrefetchUnmergedChunks.WithLabelValues(c.ImageRef).Set(float64(c.Metrics.PrefetchUnmergedChunks))
	data.CachePrefetchCumulativeTimeMillis.WithLabelValues(c.ImageRef).Set(float64(c.Metrics.PrefetchCumulativeTimeMillis))
	data.CachePrefetchTotalDurationMillis.WithLabelValues(c.ImageRef).Set(float64(prefetchTotalDuration))
	data.CacheBufferedBackendSize.WithLabelValues(c.ImageRef).Set(float64(c.Metrics.BufferedBackendSize))
}

func (c *CacheMetricsVecCollector) Collect() {
	for _, cacheMetrics := range c.MetricsVec {
		cacheMetrics.Collect()
	}
}
