/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	"time"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/collector"
	"github.com/pkg/errors"
)

func (m *Manager) SubscribeDaemonEvent(d *daemon.Daemon) error {
	if err := m.monitor.Subscribe(d.ID(), d.GetAPISock(), m.LivenessNotifier); err != nil {
		log.L.Errorf("Nydusd %s probably not started", d.ID())
		return errors.Wrapf(err, "subscribe daemon %s", d.ID())
	}
	return nil
}

func (m *Manager) UnsubscribeDaemonEvent(d *daemon.Daemon) error {
	// Starting a new nydusd will re-subscribe
	if err := m.monitor.Unsubscribe(d.ID()); err != nil {
		log.L.Warnf("fail to unsubscribe daemon %s, %v", d.ID(), err)
		return errors.Wrapf(err, "unsubscribe daemon %s", d.ID())
	}
	return nil
}

func (m *Manager) handleDaemonDeathEvent() {
	// TODO: ratelimit for daemon recovery operations?
	for ev := range m.LivenessNotifier {
		log.L.Warnf("Daemon %s died! socket path %s", ev.daemonID, ev.path)

		d := m.GetByDaemonID(ev.daemonID)
		if d == nil {
			log.L.Warnf("Daemon %s was not found", ev.daemonID)
			return
		}

		d.Lock()
		collector.NewDaemonInfoCollector(&d.Version, -1).Collect()
		d.Unlock()

		d.ResetState()

		if m.RecoverPolicy == config.RecoverPolicyRestart {
			log.L.Infof("Restart daemon %s", ev.daemonID)
			go m.doDaemonRestart(d)
		} else if m.RecoverPolicy == config.RecoverPolicyFailover {
			log.L.Infof("Do failover for daemon %s", ev.daemonID)
			go m.doDaemonFailover(d)
		}
	}
}

func (m *Manager) doDaemonFailover(d *daemon.Daemon) {
	if err := d.Wait(); err != nil {
		log.L.Warnf("fail to wait for daemon, %v", err)
	}

	// Starting a new nydusd will re-subscribe
	if err := m.UnsubscribeDaemonEvent(d); err != nil {
		log.L.Warnf("fail to unsubscribe daemon %s, %v", d.ID(), err)
	}

	su := m.SupervisorSet.GetSupervisor(d.ID())
	if err := su.SendStatesTimeout(time.Second * 10); err != nil {
		log.L.Errorf("Send states error, %s", err)
		return
	}

	// Failover nydusd still depends on the old supervisor

	if err := m.StartDaemon(d); err != nil {
		log.L.Errorf("fail to start daemon %s when recovering", d.ID())
		return
	}

	if err := d.WaitUntilState(types.DaemonStateInit); err != nil {
		log.L.WithError(err).Errorf("daemon didn't reach state %s,", types.DaemonStateInit)
		return
	}

	if err := d.TakeOver(); err != nil {
		log.L.Errorf("fail to takeover, %s", err)
		return
	}

	if err := d.Start(); err != nil {
		log.L.Errorf("fail to start service, %s", err)
		return
	}
}

func (m *Manager) doDaemonRestart(d *daemon.Daemon) {
	if err := d.Wait(); err != nil {
		log.L.Warnf("fails to wait for daemon, %v", err)
	}

	// Starting a new nydusd will re-subscribe
	if err := m.UnsubscribeDaemonEvent(d); err != nil {
		log.L.Warnf("fails to unsubscribe daemon %s, %v", d.ID(), err)
	}

	d.ClearVestige()
	if err := m.StartDaemon(d); err != nil {
		log.L.Errorf("fails to start daemon %s when recovering", d.ID())
		return
	}

	// Mount rafs instance by http API
	instances := d.RafsCache.List()
	for _, r := range instances {
		// For dedicated nydusd daemon, Rafs has already been mounted during starting nydusd
		if d.HostMountpoint() == r.GetMountpoint() {
			break
		}

		if err := d.SharedMount(r); err != nil {
			log.L.Warnf("Failed to mount rafs instance, %v", err)
		}
	}
}
