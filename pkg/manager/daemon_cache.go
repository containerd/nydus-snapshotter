/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	"sync"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
)

// Daemon state cache to speed up access.
type DaemonCache struct {
	mu            sync.Mutex
	idxByDaemonID map[string]*daemon.Daemon // index by ID
}

func newDaemonCache() *DaemonCache {
	return &DaemonCache{
		idxByDaemonID: make(map[string]*daemon.Daemon),
	}
}

// Return nil if the daemon is never inserted or managed,
// otherwise returns the previously inserted daemon pointer.
// Allowing replace an existed daemon since some fields in Daemon can change after restarting nydusd.
func (s *DaemonCache) Add(daemon *daemon.Daemon) *daemon.Daemon {
	s.mu.Lock()
	defer s.mu.Unlock()

	old := s.idxByDaemonID[daemon.ID()]
	s.idxByDaemonID[daemon.ID()] = daemon
	return old
}

func (s *DaemonCache) removeLocked(d *daemon.Daemon) *daemon.Daemon {
	old := s.idxByDaemonID[d.ID()]
	delete(s.idxByDaemonID, d.ID())
	return old
}

func (s *DaemonCache) Remove(d *daemon.Daemon) *daemon.Daemon {
	s.mu.Lock()
	old := s.removeLocked(d)
	s.mu.Unlock()

	return old
}

func (s *DaemonCache) RemoveByDaemonID(id string) *daemon.Daemon {
	return s.GetByDaemonID(id, func(d *daemon.Daemon) { s.removeLocked(d) })
}

// Also recover daemon runtime state here
func (s *DaemonCache) Update(d *daemon.Daemon) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.L.Infof("Recovering daemon ID %s", d.ID())

	s.idxByDaemonID[d.ID()] = d
}

func (s *DaemonCache) GetByDaemonID(id string, op func(d *daemon.Daemon)) *daemon.Daemon {
	var daemon *daemon.Daemon
	s.mu.Lock()
	defer s.mu.Unlock()
	daemon = s.idxByDaemonID[id]

	if daemon != nil && op != nil {
		op(daemon)
	}

	return daemon
}

func (s *DaemonCache) List() []*daemon.Daemon {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.idxByDaemonID) == 0 {
		return nil
	}

	listed := make([]*daemon.Daemon, 0, len(s.idxByDaemonID))
	for _, d := range s.idxByDaemonID {
		listed = append(listed, d)
	}

	return listed
}

func (s *DaemonCache) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.idxByDaemonID)
}
