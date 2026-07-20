/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package fs

import (
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// Exercises the collection loop (Clear/Save) concurrently with a Prometheus
// scrape (Collect). Meaningful under the race detector: the two run on
// different goroutines in the snapshotter and used to share constHists with
// no synchronization.
func TestMetricHistogramConcurrentCollectAndScrape(t *testing.T) {
	h := &MetricHistogram{
		Desc: prometheus.NewDesc("test_hist", "test histogram", nil, nil),
	}
	metric := prometheus.MustNewConstMetric(
		prometheus.NewDesc("test_metric", "test metric", nil, nil),
		prometheus.CounterValue, 1,
	)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 1000 {
			h.Clear()
			h.Save(metric)
			h.Save(metric)
		}
	}()
	go func() {
		defer wg.Done()
		ch := make(chan prometheus.Metric, 16)
		done := make(chan struct{})
		go func() {
			for range ch {
			}
			close(done)
		}()
		for range 1000 {
			h.Collect(ch)
		}
		close(ch)
		<-done
	}()
	wg.Wait()
}
