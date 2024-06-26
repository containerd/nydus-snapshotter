/*
 * Copyright (c) 2021. Ant Group. All rights reserved.
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package metrics

import (
	"fmt"
	"net"
	"net/http"

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/registry"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Endpoint for prometheus metrics
var endpointPromMetrics = "/v1/metrics"

func trapClosedConnErr(err error) error {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// NewListener creates a new TCP listener bound to the given address.
func NewMetricsHTTPListenerServer(addr string) error {
	if addr == "" {
		return fmt.Errorf("the address for metrics HTTP server is invalid")
	}

	http.Handle(endpointPromMetrics, promhttp.HandlerFor(registry.Registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.HTTPErrorOnError,
	}))

	l, err := net.Listen("tcp", addr)
	if err != nil {
		return errors.Wrapf(err, "metrics server listener, addr=%s", addr)
	}

	go func() {
		if err := http.Serve(l, nil); trapClosedConnErr(err) != nil {
			log.L.Errorf("Metrics server fails to listen or serve %s: %v", addr, err)
		}
	}()

	return nil
}
