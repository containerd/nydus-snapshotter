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
	"path/filepath"
	"time"

	"github.com/pkg/errors"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/collector"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/exporter"
)

type ServerOpt func(*Server) error

type Server struct {
	rootDir     string
	metricsFile string
	pm          *manager.Manager
	fsCollector *collector.FsMetricsCollector
	snCollector *collector.SnapshotterMetricsCollector
}

func WithRootDir(rootDir string) ServerOpt {
	return func(s *Server) error {
		s.rootDir = rootDir
		return nil
	}
}

func WithMetricsFile(metricsFile string) ServerOpt {
	return func(s *Server) error {
		if s.rootDir == "" {
			return errors.New("root dir is required")
		}

		if metricsFile == "" {
			metricsFile = filepath.Join(s.rootDir, "metrics.log")
		}

		s.metricsFile = metricsFile
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

	s.fsCollector = collector.NewFsMetricsCollector(nil, "")
	snCollector, err := collector.NewSnapshotterMetricsCollector(ctx, s.pm.CacheDir(), os.Getpid())
	if err != nil {
		return nil, errors.Wrap(err, "new snapshotter metrics collector failed")
	}
	s.snCollector = snCollector

	if err := exporter.NewFileExporter(
		exporter.WithOutputFile(s.metricsFile),
	); err != nil {
		return nil, errors.Wrap(err, "new metric exporter failed")
	}

	return &s, nil
}

func (s *Server) CollectFsMetrics(ctx context.Context) {
	// Collect FS metrics from daemons.
	daemons := s.pm.ListDaemons()
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
			s.fsCollector.Metrics = fsMetrics
			s.fsCollector.ImageRef = i.ImageID

			s.fsCollector.Collect()
		}

	}
}

func (s *Server) StartCollectMetrics(ctx context.Context, enableMetrics bool) error {
	// TODO(renzhen): make collect interval time configurable
	timer := time.NewTicker(time.Duration(1) * time.Minute)

outer:
	for {
		select {
		case <-timer.C:
			if enableMetrics {
				s.CollectFsMetrics(ctx)
			}

			// Collect snapshotter metrics.
			s.snCollector.Collect()
		case <-ctx.Done():
			log.G(ctx).Infof("cancel daemon metrics collecting")
			break outer
		}
	}

	return nil
}
