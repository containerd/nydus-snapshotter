/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package data

import (
	"github.com/containerd/nydus-snapshotter/pkg/metrics/types/ttl"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// CredentialRenewals counts credential renewal attempts per image ref,
	// labeled by outcome ("success" or "failure").
	CredentialRenewals = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "snapshotter_credential_renewals_total",
			Help: "Total number of credential renewal attempts, labeled by image ref and result (success or failure).",
		},
		[]string{imageRefLabel, credentialResultLabel},
	)

	// CredentialStoreEntries tracks the number of credentials currently held
	// in the renewal store per image ref. Uses TTL so evicted refs disappear
	// from the metric automatically.
	CredentialStoreEntries = ttl.NewGaugeVecWithTTL(
		prometheus.GaugeOpts{
			Name: "snapshotter_credential_store_entries",
			Help: "Number of credentials currently tracked in the renewal store per image ref.",
		},
		[]string{imageRefLabel},
		ttl.DefaultTTL,
	)

)
