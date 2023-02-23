/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package data

import (
	"github.com/containerd/nydus-snapshotter/pkg/metrics/types/ttl"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	nydusdEventLabel   = "nydusd_event"
	nydusdVersionLabel = "version"
	daemonIDLabel      = "daemon_id"
)

var (
	NydusdEventCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nydusd_lifetime_event_counts",
			Help: "The lifetime events of nydus daemon.",
		},
		[]string{nydusdEventLabel},
	)
	NydusdCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nydusd_counts",
			Help: "The counts of nydus daemon.",
		},
		[]string{nydusdVersionLabel},
	)
	NydusdRSS = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "nydusd_rss_kilobytes",
			Help: "Memory usage (RSS) of nydus daemon.",
		},
		[]string{daemonIDLabel},
		ttl.DefaultTTL,
	)
)
