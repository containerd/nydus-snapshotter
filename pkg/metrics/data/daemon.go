/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package data

import "github.com/prometheus/client_golang/prometheus"

var (
	nydusdEventLabel = "nydusd_event"
)

var (
	NydusdEventCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nydusd_lifetime_event_counts",
			Help: "The lifetime events of nydus daemon.",
		},
		[]string{nydusdEventLabel},
	)
)
