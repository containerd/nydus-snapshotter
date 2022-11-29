/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package data

import "github.com/prometheus/client_golang/prometheus"

var (
	daemonIDLabel = "daemon_id"
	timeLabel     = "time"
	eventLabel    = "event"
)

var (
	NydusdEvent = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nydusd_lifetime_events",
			Help: "The lifetime events of nydus daemon.",
		},
		[]string{daemonIDLabel, timeLabel, eventLabel},
	)
)
