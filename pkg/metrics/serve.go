/*
 * Copyright (c) 2021. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package metrics

import (
	"context"
	"os"
	"time"

	"github.com/pkg/errors"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/collector"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/tool"
)

// Default interval to determine a hung IO.
const defaultHungIOInterval = 10 * time.Second

type ServerOpt func(*Server) error

type Server struct {
	managers          []*manager.Manager
	snCollectors      []*collector.SnapshotterMetricsCollector
	fsCollector       *collector.FsMetricsVecCollector
	inflightCollector *collector.InflightMetricsVecCollector
}

func WithProcessManager(pm *manager.Manager) ServerOpt {
	return func(s *Server) error {
		if pm != nil {
			s.managers = append(s.managers, pm)
		}
		return nil
	}
}

func NewServer(ctx context.Context, opts ...ServerOpt) (*Server, error) {
	var s Server
	for _, o := range opts {
		if err := o(&s); err != nil {
			return nil, err
		}
	}

	s.fsCollector = collector.NewFsMetricsVecCollector()
	// TODO(tangbin): make hung IO interval configurable
	s.inflightCollector = collector.NewInflightMetricsVecCollector(defaultHungIOInterval)
	for _, pm := range s.managers {
		snCollector, err := collector.NewSnapshotterMetricsCollector(ctx, pm.CacheDir(), os.Getpid())
		if err != nil {
			return nil, errors.Wrap(err, "new snapshotter metrics collector failed")
		}
		s.snCollectors = append(s.snCollectors, snCollector)
	}

	return &s, nil
}

func (s *Server) CollectDaemonResourceMetrics(ctx context.Context) {
	var daemonResource collector.DaemonResourceCollector
	for _, pm := range s.managers {
		// Collect daemon resource usage metrics.
		daemons := pm.ListDaemons()
		for _, d := range daemons {
			memRSS, err := tool.GetProcessMemoryRSSKiloBytes(d.Pid())
			if err != nil {
				log.L.Warnf("Failed to get daemon %s RSS memory", d.ID())
			}

			daemonResource.DaemonID = d.ID()
			daemonResource.Value = memRSS
			daemonResource.Collect()
		}
	}
}

func (s *Server) CollectFsMetrics(ctx context.Context) {
	var fsMetricsVec []collector.FsMetricsCollector

	for _, pm := range s.managers {
		// Collect FS metrics from fusedev daemons.
		if pm.FsDriver != config.FsDriverFusedev {
			continue
		}

		daemons := pm.ListDaemons()
		for _, d := range daemons {
			// Skip daemons that are not serving
			if d.State() != types.DaemonStateRunning {
				continue
			}

			for _, i := range d.RafsCache.List() {
				var sid string

				if d.IsSharedDaemon() {
					sid = i.SnapshotID
				} else {
					sid = ""
				}

				fsMetrics, err := d.GetFsMetrics(sid)
				if err != nil {
					log.G(ctx).Errorf("failed to get fs metric: %v", err)
					continue
				}

				fsMetricsVec = append(fsMetricsVec, collector.FsMetricsCollector{
					Metrics:  fsMetrics,
					ImageRef: i.ImageID,
				})
			}
		}
	}

	if fsMetricsVec != nil {
		s.fsCollector.MetricsVec = fsMetricsVec
		s.fsCollector.Collect()
	}
}

func (s *Server) CollectInflightMetrics(ctx context.Context) {
	inflightMetricsVec := make([]*types.InflightMetrics, 0, 16)
	for _, pm := range s.managers {
		// Collect inflight metrics from fusedev daemons.
		if pm.FsDriver != config.FsDriverFusedev {
			continue
		}

		daemons := pm.ListDaemons()
		for _, d := range daemons {

			// Only count for daemon that is serving
			if d.State() != types.DaemonStateRunning {
				continue
			}

			inflightMetrics, err := d.GetInflightMetrics()
			if err != nil {
				log.G(ctx).Errorf("failed to get inflight metric: %v", err)
				continue
			}
			inflightMetricsVec = append(inflightMetricsVec, inflightMetrics)
		}
	}

	if inflightMetricsVec != nil {
		s.inflightCollector.MetricsVec = inflightMetricsVec
		s.inflightCollector.Collect()
	}
}

func (s *Server) StartCollectMetrics(ctx context.Context) error {
	// TODO(renzhen): make collect interval time configurable
	timer := time.NewTicker(time.Duration(1) * time.Minute)
	// The timer period is the same as the interval for determining hung IOs.
	//
	// Since the elapsed time of hung IO is configuration dependent,
	// e.g. timeout * retry times when the backend is a registry.
	// Therefore, we cannot get complete hung IO data after 1 minute.
	InflightTimer := time.NewTicker(s.inflightCollector.HungIOInterval)

outer:
	for {
		select {
		case <-timer.C:
			s.CollectFsMetrics(ctx)
			s.CollectDaemonResourceMetrics(ctx)
			// Collect snapshotter metrics.
			for _, snCollector := range s.snCollectors {
				snCollector.Collect()
			}
		case <-InflightTimer.C:
			s.CollectInflightMetrics(ctx)
		case <-ctx.Done():
			log.G(ctx).Infof("cancel metrics collecting")
			break outer
		}
	}

	return nil
}
