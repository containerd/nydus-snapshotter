/*
 * Copyright (c) 2021. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package metrics

import (
	"context"
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

	err := exporter.NewFileExporter(
		exporter.WithOutputFile(s.metricsFile),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to new metric exporter")
	}

	return &s, nil
}

func (s *Server) CollectDaemonMetrics(ctx context.Context) error {
	// TODO(renzhen): make collect interval time configurable
	timer := time.NewTicker(time.Duration(1) * time.Minute)

outer:
	for {
		select {
		case <-timer.C:
			// Collect metrics from daemons.
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

					if err := collector.CollectFsMetrics(fsMetrics, i.ImageID); err != nil {
						log.G(ctx).Errorf("failed to export fs metrics for %s: %v", i.ImageID, err)
						continue
					}
				}

			}
		case <-ctx.Done():
			log.G(ctx).Infof("cancel daemon metrics collecting")
			break outer
		}
	}

	return nil
}
