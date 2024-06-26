/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/collector"
	"github.com/containerd/nydus-snapshotter/pkg/utils/retry"
)

// LivenessMonitor liveness of a nydusd daemon.
type LivenessMonitor interface {
	// Subscribe death event of a nydusd daemon.
	// `path` is where the monitor is listening on.
	Subscribe(id string, path string, notifier chan<- deathEvent) error
	// Unsubscribe death event of a nydusd daemon.
	Unsubscribe(id string) error
	// Run the monitor, wait for nydusd death event.
	Run()
	// Stop the monitor and release all the resources.
	Destroy()
}

type target struct {
	// A connection to the nydusd. Should close it when stopping the liveness monitor!
	uc *net.UnixConn
	// Notify subscriber that the nydusd is dead via the channel
	notifier chan<- deathEvent
	// `id` is usually the daemon ID
	id   string
	path string
}

type FD = uintptr

type livenessMonitor struct {
	mu sync.Mutex
	// Get a subscribing target by the target ID (usually is the daemon ID)
	subscribers map[string]*target
	// Get a subscribing target by the connection FD. Each liveness
	// probe has a unique connection FD which is listened for epoll event.
	set     map[FD]*target
	epollFd int
}

type deathEvent struct {
	daemonID string
	path     string
}

func newMonitor() (_ *livenessMonitor, err error) {
	var epollFd int
	epollFd, err = unix.EpollCreate1(unix.EPOLL_CLOEXEC)
	if err != nil {
		return nil, errors.Wrap(err, "create daemons monitor")
	}

	m := &livenessMonitor{
		epollFd:     epollFd,
		subscribers: make(map[string]*target),
		set:         make(map[uintptr]*target),
	}

	return m, nil
}

func (m *livenessMonitor) Subscribe(id string, path string, notifier chan<- deathEvent) (err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.subscribers[id]; ok && s.path == path {
		log.L.Warnf("Daemon %s is already subscribed!", id)
		return errdefs.ErrAlreadyExists
	}

	var (
		c       net.Conn
		rawConn syscall.RawConn
	)

	err = retry.Do(func() (err error) {
		// Don't forget close me!
		if c, err = net.Dial("unix", path); err != nil {
			log.L.Errorf("Fails to connect to %s, %v", path, err)
			return
		}

		return nil
	},
		retry.LastErrorOnly(true),
		retry.Attempts(20), // totally wait for 2 seconds, should be enough
		retry.Delay(100*time.Millisecond))

	if err != nil {
		return err
	}

	uc, ok := c.(*net.UnixConn)
	if !ok {
		return errors.Errorf("a unix socket connection is required")
	}

	if rawConn, err = uc.SyscallConn(); err != nil {
		return
	}

	err = rawConn.Control(func(fd FD) {
		err = unix.SetNonblock(int(fd), true)
		if err != nil {
			log.L.Errorf("Failed to set file. daemon id %s path %s. %v", id, path, err)
			return
		}

		event := unix.EpollEvent{
			Fd:     int32(fd),
			Events: unix.EPOLLHUP | unix.EPOLLERR | unix.EPOLLET,
		}

		err = unix.EpollCtl(m.epollFd, unix.EPOLL_CTL_ADD, int(fd), &event)
		if err != nil {
			log.L.Errorf("Failed to control epoll. daemon id %s path %s. %v", id, path, err)
			return
		}
		target := &target{uc: uc, id: id, path: path}

		// Only add subscribed target when everything is OK.
		m.set[fd] = target
		m.subscribers[id] = target
		target.notifier = notifier
	})

	log.L.Infof("Subscribe daemon %s liveness event, path=%s.", id, path)

	return
}

func (m *livenessMonitor) Unsubscribe(id string) (err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.unsubscribe(id)
}

func (m *livenessMonitor) unsubscribe(id string) (err error) {
	target, ok := m.subscribers[id]
	if !ok {
		return errdefs.ErrNotFound
	}

	delete(m.subscribers, id)

	var rawConn syscall.RawConn
	if rawConn, err = target.uc.SyscallConn(); err != nil {
		log.L.Errorf("Fail to access underlying FD, id=%s", id)
		return
	}

	// No longer wait for event, delete it from interest list.
	if err = rawConn.Control(func(fd uintptr) {
		if err := unix.EpollCtl(m.epollFd, unix.EPOLL_CTL_DEL, int(fd), &unix.EpollEvent{}); err != nil {
			log.L.Errorf("Fail to delete event fd %d for supervisor %s", int(fd), id)
			return
		}
		delete(m.set, fd)
	}); err != nil {
		return errors.Wrapf(err, "remove target FD in the interested list, id=%s", id)
	}

	if err = target.uc.Close(); err != nil {
		log.L.Errorf("Fails to close unix connection for daemon %s", id)
		return
	}

	return nil
}

func (m *livenessMonitor) Run() {
	var events [512]unix.EpollEvent
	go func() {
		defer log.L.Infof("Exiting liveness monitor")
		log.L.Infof("Run daemons monitor...")
		for {
			n, err := unix.EpollWait(m.epollFd, events[:], -1)
			if err != nil {
				if err == unix.EINTR {
					continue
				}

				// `Destroy` should close the epoll fd thus to exit the goroutine.
				log.L.Errorf("Monitor fails to wait events, %v. Exiting!", err)
				return
			}

			for i := 0; i < n; i++ {
				ev := events[i]

				m.mu.Lock()
				target, ok := m.set[uintptr(ev.Fd)]
				m.mu.Unlock()
				// There is a race that it is waken before unsubscribing,
				// so the target can't be found.
				if !ok {
					continue
				}

				if ev.Events&(unix.EPOLLHUP|unix.EPOLLERR) != 0 {
					log.L.Warnf("Daemon %s died", target.id)
					collector.NewDaemonEventCollector(types.DaemonStateDied).Collect()
					// Notify subscribers that death event happens
					target.notifier <- deathEvent{daemonID: target.id, path: target.path}
				}
			}
		}
	}()
}

func (m *livenessMonitor) Destroy() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.subscribers {
		if err := m.unsubscribe(i); err != nil {
			log.L.Warnf("fail to unsubscribe %s", i)
		}
	}

	if m.epollFd > 0 {
		// Closing epoll fd does not waken `EpollWait`. So ending events loop can not be
		// done via closing the file. But liveness monitor is running with nydus-snapshotter
		// in the whole life. So we don't stop the loop now.
		unix.Close(m.epollFd)
	}
}
