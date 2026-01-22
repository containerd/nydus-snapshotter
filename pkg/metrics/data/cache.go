/*
 * Copyright (c) 2025. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package data

import (
	"github.com/containerd/nydus-snapshotter/pkg/metrics/types/ttl"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	CachePartialHits = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_cache_partial_hits",
			Help: "Number of partial cache hits (IO needs a part of the chunk)",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)

	CacheWholeHits = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_cache_whole_hits",
			Help: "Number of whole cache hits (IO needs the entire chunk)",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)

	CacheTotalRequests = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_cache_total_requests",
			Help: "Total number of cache read requests. Cache hit percentage = (partial_hits + whole_hits) / total",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)

	CacheEntriesCount = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_cache_entries_count",
			Help: "Number of chunks in ready status",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)

	CachePrefetchDataBytes = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_cache_prefetch_data_bytes",
			Help: "Total amount of data prefetched, in bytes",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)

	CachePrefetchRequestsCount = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_cache_prefetch_requests_count",
			Help: "Total prefetch requests issued from storage/blobs or rafs filesystem layer for each file that needs prefetch",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)

	CachePrefetchWorkers = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_cache_prefetch_workers",
			Help: "Number of prefetch workers",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)

	CachePrefetchUnmergedChunks = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_cache_prefetch_unmerged_chunks",
			Help: "Number of unmerged chunks",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)

	CachePrefetchCumulativeTimeMillis = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_cache_prefetch_cumulative_time_millis",
			Help: "Cumulative time latencies in milliseconds of each prefetch request which can be handled in parallel. It starts when the request is born including nydusd processing and schedule and end when the chunk is downloaded and stored. The average prefetch latency can be calculated by `prefetch_cumulative_time_millis / prefetch_requests_count`",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)

	CachePrefetchTotalDurationMillis = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_cache_prefetch_duration_millis",
			Help: "Total wall clock duration of the prefetch, in milliseconds",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)

	CacheBufferedBackendSize = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_cache_buffered_backend_size",
			Help: "Size of the buffered backend, in bytes",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)
)
