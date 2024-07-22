/*
 * Copyright (c) 2021. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package ttl

import (
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
)

func TestNewGaugeVecWithTTL(t *testing.T) {
	defaultCleanUpPeriod = 5 * time.Second
	g := NewGaugeVecWithTTL(prometheus.GaugeOpts{
		Name: "nydusd_fuse_connection_waiting_count",
		Help: "nydusd_fuse_connection_waiting_count",
	},
		[]string{"daemon_id"},
		3*time.Second,
	)
	g.WithLabelValues("value1").Set(10)
	g.WithLabelValues("value2").Set(10)
	metricsCh := make(chan prometheus.Metric, 2)
	go g.Collect(metricsCh)

	var metricsSlice []*dto.Metric
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		for m := range metricsCh {
			var metrics dto.Metric
			err := m.Write(&metrics)
			assert.Nil(t, err)
			metricsSlice = append(metricsSlice, &metrics)
			wg.Done()
		}
	}()
	wg.Wait()
	assert.Equal(t, 2, len(metricsSlice))
	metricsSlice = []*dto.Metric{}

	time.Sleep(3 * time.Second)
	g.WithLabelValues("value1").Set(10)
	g.mu.Lock()
	assert.Equal(t, 2, len(g.labelValueMap))
	g.mu.Unlock()
	time.Sleep(3 * time.Second)
	g.mu.Lock()
	assert.Equal(t, 1, len(g.labelValueMap))
	g.mu.Unlock()
	metricsCh = make(chan prometheus.Metric, 2)
	go g.Collect(metricsCh)
	go func() {
		for m := range metricsCh {
			var metrics dto.Metric
			err := m.Write(&metrics)
			assert.Nil(t, err)
			mu.Lock()
			metricsSlice = append(metricsSlice, &metrics)
			mu.Unlock()
		}
	}()

	time.Sleep(6 * time.Second)
	mu.Lock()
	assert.Equal(t, 1, len(metricsSlice))
	mu.Unlock()
	g.mu.Lock()
	assert.Equal(t, 0, len(g.labelValueMap))
	g.mu.Unlock()
}
