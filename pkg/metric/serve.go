/*
 * Copyright (c) 2021. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package metrics

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	"github.com/containerd/nydus-snapshotter/pkg/metric/exporter"
)

type ServerOpt func(*Server) error

const sockFileName = "metrics.sock"

type Server struct {
	listener    net.Listener
	rootDir     string
	metricsFile string
	pm          *manager.Manager
	exp         *exporter.Exporter
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

	exp, err := exporter.NewExporter(
		exporter.WithOutputFile(s.metricsFile),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to new metric exporter")
	}
	s.exp = exp

	sockPath := filepath.Join(s.rootDir, sockFileName)

	if _, err := os.Stat(sockPath); err == nil {
		err = os.Remove(sockPath)
		if err != nil {
			return nil, err
		}
	}
	ln, err := NewListener(sockPath)
	if err != nil {
		return nil, err
	}
	s.listener = ln

	log.G(ctx).Infof("Starting metrics server on %s", sockPath)

	return &s, nil
}

func (s *Server) collectDaemonMetric(ctx context.Context) {
	// TODO(renzhen): make collect interval time configurable
	timer := time.NewTicker(time.Duration(1) * time.Minute)

outer:
	for {
		select {
		case <-timer.C:
			daemons := s.pm.ListDaemons()
			for _, d := range daemons {
				if d.ID == daemon.SharedNydusDaemonID {
					continue
				}
				fsMetrics, err := d.GetFsMetric(s.pm.IsSharedDaemon(), d.SnapshotID)
				if err != nil {
					log.G(ctx).Errorf("failed to get fs metric: %v", err)
					continue
				}

				if err := s.exp.ExportFsMetrics(fsMetrics, d.ImageID); err != nil {
					log.G(ctx).Errorf("failed to export fs metrics for %s: %v", d.ImageID, err)
					continue
				}
			}
		case <-ctx.Done():
			log.G(ctx).Infof("cancel daemon metrics collecting")
			break outer
		}
	}
}

func (s *Server) Serve(ctx context.Context) error {
	// Start to collect metrics from daemons periodically.
	go func() {
		s.collectDaemonMetric(ctx)
	}()

	return nil
}
