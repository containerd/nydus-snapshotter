/*
 * Copyright (c) 2021. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package metrics

import (
	"fmt"
	"net/http"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/registry"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Endpoint for prometheus metrics
var endpointPromMetrics = "/v1/metrics"

// NewMetricsHTTPListener creates a new TCP listener bound to the given address for metrics server.
func NewMetricsHTTPListener(addr string) error {
	if addr == "" {
		return fmt.Errorf("the address for metrics HTTP server is invalid")
	}

	http.Handle(endpointPromMetrics, promhttp.HandlerFor(registry.Registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.HTTPErrorOnError,
	}))

	log.L.Infof("Start metrics HTTP server on %s", addr)

	if err := http.ListenAndServe(addr, nil); err != nil {
		return fmt.Errorf("error serve on %s: %v", addr, err)
	}

	return nil
}
