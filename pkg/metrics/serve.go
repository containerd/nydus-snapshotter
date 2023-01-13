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
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/collector"
)

// Default interval to determine a hung IO.
const defaultHungIOInterval = 10 * time.Second

type ServerOpt func(*Server) error

type Server struct {
	rootDir           string
	pm                *manager.Manager
	fsCollector       *collector.FsMetricsVecCollector
	inflightCollector *collector.InflightMetricsVecCollector
	snCollector       *collector.SnapshotterMetricsCollector
}

func WithRootDir(rootDir string) ServerOpt {
	return func(s *Server) error {
		s.rootDir = rootDir
		return nil
	}
}

func WithProcessManager(pm *manager.Manager) ServerOpt {
	return func(s *Server) error {
		s.pm = pm
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
	snCollector, err := collector.NewSnapshotterMetricsCollector(ctx, s.pm.CacheDir(), os.Getpid())
	if err != nil {
		return nil, errors.Wrap(err, "new snapshotter metrics collector failed")
	}
	s.snCollector = snCollector

	return &s, nil
}

func (s *Server) CollectFsMetrics(ctx context.Context) {
	// Collect FS metrics from daemons.
	daemons := s.pm.ListDaemons()
	var fsMetricsVec []collector.FsMetricsCollector
	for _, d := range daemons {
		for _, i := range d.Instances.List() {
			var sid string

			if i.GetMountpoint() == d.HostMountpoint() {
				sid = ""
			} else {
				sid = i.SnapshotID
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
	if fsMetricsVec != nil {
		s.fsCollector.MetricsVec = fsMetricsVec
		s.fsCollector.Collect()
	}
}

func (s *Server) CollectInflightMetrics(ctx context.Context) {
	// Collect inflight metrics from daemons.
	daemons := s.pm.ListDaemons()
	var inflightMetricsVec []*types.InflightMetrics
	for _, d := range daemons {
		inflightMetrics, err := d.GetInflightMetrics()
		if err != nil {
			log.G(ctx).Errorf("failed to get inflight metric: %v", err)
			continue
		}
		inflightMetricsVec = append(inflightMetricsVec, inflightMetrics)
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
			// Collect FS metrics.
			s.CollectFsMetrics(ctx)
			// Collect snapshotter metrics.
			s.snCollector.Collect()
		case <-InflightTimer.C:
			// Collect inflight metrics.
			s.CollectInflightMetrics(ctx)
		case <-ctx.Done():
			log.G(ctx).Infof("cancel metrics collecting")
			break outer
		}
	}

	return nil
}
