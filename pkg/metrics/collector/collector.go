/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package collector

import "github.com/containerd/nydus-snapshotter/pkg/daemon/types"

type Collector interface {
	// Collect metrics to data.
	Collect()
}

func CollectDaemonEvent(daemonID string, event string) error {
	GlobalDaemonEventCollector.Collect(daemonID, event)
	return nil
}

func CollectFsMetrics(m *types.FsMetrics, imageRef string) error {
	GlobalFsMetricsCollector.Collect(m, imageRef)
	return nil
}
