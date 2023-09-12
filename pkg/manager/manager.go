/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"sync"

	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/config/daemonconfig"
	"github.com/containerd/nydus-snapshotter/pkg/cgroup"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/collector"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"
	"github.com/containerd/nydus-snapshotter/pkg/store"
	"github.com/containerd/nydus-snapshotter/pkg/supervisor"
)

// Manage RAFS filesystem instances and nydusd daemons.
type Manager struct {
	// Protect fields `store` and `daemonStates`
	mu       sync.Mutex
	cacheDir string
	FsDriver string
	store    Store

	// Fields below are used to manage nydusd daemons.
	//
	// The `daemonCache` is cache for nydusd daemons stored in `store`.
	// You should update `store` first before modifying cached state.
	daemonCache      *DaemonCache
	DaemonConfig     *daemonconfig.DaemonConfig // Daemon configuration template.
	CgroupMgr        *cgroup.Manager
	monitor          LivenessMonitor
	LivenessNotifier chan deathEvent // TODO: Close me
	NydusdBinaryPath string
	RecoverPolicy    config.DaemonRecoverPolicy
	SupervisorSet    *supervisor.SupervisorsSet
}

type Opt struct {
	CacheDir         string
	CgroupMgr        *cgroup.Manager
	DaemonConfig     *daemonconfig.DaemonConfig
	Database         *store.Database
	FsDriver         string
	NydusdBinaryPath string
	RecoverPolicy    config.DaemonRecoverPolicy
	RootDir          string // Nydus-snapshotter work directory
}

func NewManager(opt Opt) (*Manager, error) {
	s, err := store.NewDaemonRafsStore(opt.Database)
	if err != nil {
		return nil, err
	}

	monitor, err := newMonitor()
	if err != nil {
		return nil, errors.Wrap(err, "create daemons liveness monitor")
	}

	var supervisorSet *supervisor.SupervisorsSet
	if opt.RecoverPolicy == config.RecoverPolicyFailover {
		supervisorSet, err = supervisor.NewSupervisorSet(filepath.Join(opt.RootDir, "supervisor"))
		if err != nil {
			return nil, errors.Wrap(err, "create supervisor set")
		}
	}

	mgr := &Manager{
		store:            s,
		NydusdBinaryPath: opt.NydusdBinaryPath,
		cacheDir:         opt.CacheDir,
		daemonCache:      newDaemonCache(),
		monitor:          monitor,
		LivenessNotifier: make(chan deathEvent, 32),
		RecoverPolicy:    opt.RecoverPolicy,
		SupervisorSet:    supervisorSet,
		DaemonConfig:     opt.DaemonConfig,
		CgroupMgr:        opt.CgroupMgr,
		FsDriver:         opt.FsDriver,
	}

	// FIXME: How to get error if monitor goroutine terminates with error?
	// TODO: Shutdown monitor immediately after snapshotter receive Exit signal
	mgr.monitor.Run()
	go mgr.handleDaemonDeathEvent()

	return mgr, nil
}

func (m *Manager) Lock() {
	m.mu.Lock()
}

func (m *Manager) Unlock() {
	m.mu.Unlock()
}

func (m *Manager) CacheDir() string {
	return m.cacheDir
}

// Recover nydusd daemons and RAFS instances on startup.
//
// To be safe:
// - Never ever delete any records from DB
// - Only cache daemon information from DB, do not actually start/create daemons
// - Only cache RAFS instance information from DB, do not actually recover RAFS runtime state.
func (m *Manager) Recover(ctx context.Context,
	recoveringDaemons *map[string]*daemon.Daemon, liveDaemons *map[string]*daemon.Daemon) error {
	if err := m.recoverDaemons(ctx, recoveringDaemons, liveDaemons); err != nil {
		return errors.Wrapf(err, "recover nydusd daemons")
	}
	if err := m.recoverRafsInstances(ctx, recoveringDaemons, liveDaemons); err != nil {
		return errors.Wrapf(err, "recover RAFS instances")
	}
	return nil
}

func (m *Manager) AddRafsInstance(r *rafs.Rafs) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	seq, err := m.store.NextInstanceSeq()
	if err != nil {
		return err
	}

	r.Seq = seq

	return m.store.AddRafsInstance(r)
}

func (m *Manager) RemoveRafsInstance(snapshotID string) error {
	return m.store.DeleteRafsInstance(snapshotID)
}

func (m *Manager) recoverRafsInstances(ctx context.Context,
	recoveringDaemons *map[string]*daemon.Daemon, liveDaemons *map[string]*daemon.Daemon) error {
	if err := m.store.WalkRafsInstances(ctx, func(r *rafs.Rafs) error {
		if r.GetFsDriver() != m.FsDriver {
			return nil
		}

		log.L.Debugf("found RAFS instance %#v", r)
		if r.GetFsDriver() == config.FsDriverFscache || r.GetFsDriver() == config.FsDriverFusedev {
			d := (*recoveringDaemons)[r.DaemonID]
			if d != nil {
				d.AddRafsInstance(r)
			}
			d = (*liveDaemons)[r.DaemonID]
			if d != nil {
				d.AddRafsInstance(r)
			}
			rafs.RafsGlobalCache.Add(r)
		} else if r.GetFsDriver() == config.FsDriverBlockdev {
			rafs.RafsGlobalCache.Add(r)
		}

		return nil
	}); err != nil {
		return errors.Wrapf(err, "walk instances to reconnect")
	}

	return nil
}

// Add an instantiated daemon to be managed by the manager.
//
// Return ErrAlreadyExists if a daemon with the same daemon ID already exists.
func (m *Manager) AddDaemon(daemon *daemon.Daemon) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if old := m.daemonCache.GetByDaemonID(daemon.ID(), nil); old != nil {
		return errdefs.ErrAlreadyExists
	}
	if err := m.store.AddDaemon(daemon); err != nil {
		return errors.Wrapf(err, "add daemon %s", daemon.ID())
	}
	m.daemonCache.Add(daemon)
	return nil
}

func (m *Manager) UpdateDaemon(daemon *daemon.Daemon) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.UpdateDaemonLocked(daemon)
}

// Notice: updating daemon states cache and DB should be protect by `mu` lock
func (m *Manager) UpdateDaemonLocked(daemon *daemon.Daemon) error {
	if old := m.daemonCache.GetByDaemonID(daemon.ID(), nil); old == nil {
		return errdefs.ErrNotFound
	}
	if err := m.store.UpdateDaemon(daemon); err != nil {
		return errors.Wrapf(err, "update daemon state for %s", daemon.ID())
	}
	m.daemonCache.Add(daemon)
	return nil
}

func (m *Manager) DeleteDaemon(daemon *daemon.Daemon) error {
	if daemon == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.store.DeleteDaemon(daemon.ID()); err != nil {
		return errors.Wrapf(err, "delete daemon state for %s", daemon.ID())
	}
	m.daemonCache.Remove(daemon)
	return nil
}

func (m *Manager) GetByDaemonID(id string) *daemon.Daemon {
	return m.daemonCache.GetByDaemonID(id, nil)
}

func (m *Manager) ListDaemons() []*daemon.Daemon {
	return m.daemonCache.List()
}

// FIXME: should handle the inconsistent status caused by any step
// in the function that returns an error.
func (m *Manager) DestroyDaemon(d *daemon.Daemon) error {
	log.L.Infof("Destroy nydusd daemon %s. Host mountpoint %s", d.ID(), d.HostMountpoint())

	// First remove the record from DB, so any failures below won't cause stale records in DB.
	if err := m.DeleteDaemon(d); err != nil {
		return errors.Wrapf(err, "delete daemon %s", d.ID())
	}

	defer m.cleanUpDaemonResources(d)

	if err := d.UmountRafsInstances(); err != nil {
		log.L.Errorf("Failed to detach all fs instances from daemon %s, %s", d.ID(), err)
	}

	if err := m.UnsubscribeDaemonEvent(d); err != nil {
		log.L.Warnf("Unable to unsubscribe, daemon ID %s, %s", d.ID(), err)
	}

	if m.SupervisorSet != nil {
		if err := m.SupervisorSet.DestroySupervisor(d.ID()); err != nil {
			log.L.Warnf("Failed to delete supervisor for daemon %s, %s", d.ID(), err)
		}
	}

	// Graceful nydusd termination will umount itself.
	if err := d.Terminate(); err != nil {
		log.L.Warnf("Fails to terminate daemon, %v", err)
	}

	if err := d.Wait(); err != nil {
		log.L.Warnf("Failed to wait for daemon, %v", err)
	}

	collector.NewDaemonEventCollector(types.DaemonStateDestroyed).Collect()
	d.Lock()
	collector.NewDaemonInfoCollector(&d.Version, -1).Collect()
	d.Unlock()

	return nil
}

func (m *Manager) cleanUpDaemonResources(d *daemon.Daemon) {
	// TODO: use recycle bin to stage directories/files to be deleted.
	resource := []string{d.States.ConfigDir, d.States.LogDir}
	if !d.IsSharedDaemon() {
		socketDir := path.Dir(d.GetAPISock())
		resource = append(resource, socketDir)
	}

	for _, dir := range resource {
		if err := os.RemoveAll(dir); err != nil {
			log.L.Errorf("failed to remove dir %s err %v", dir, err)
		}
	}

	log.L.Infof("Deleting resources %v", resource)
}

func (m *Manager) recoverDaemons(ctx context.Context,
	recoveringDaemons *map[string]*daemon.Daemon, liveDaemons *map[string]*daemon.Daemon) error {
	if err := m.store.WalkDaemons(ctx, func(s *daemon.ConfigState) error {
		if s.FsDriver != m.FsDriver {
			return nil
		}

		log.L.Debugf("found daemon states %#v", s)
		opt := make([]daemon.NewDaemonOpt, 0)
		var d, _ = daemon.NewDaemon(opt...)
		d.States = *s

		m.daemonCache.Update(d)

		if m.SupervisorSet != nil {
			su := m.SupervisorSet.NewSupervisor(d.ID())
			if su == nil {
				return errors.Errorf("create supervisor for daemon %s", d.ID())
			}
			d.Supervisor = su
		}

		if d.States.FsDriver == config.FsDriverFusedev {
			cfg, err := daemonconfig.NewDaemonConfig(d.States.FsDriver, d.ConfigFile(""))
			if err != nil {
				log.L.Errorf("Failed to reload daemon configuration %s, %s", d.ConfigFile(""), err)
				return err
			}

			d.Config = cfg
		}

		state, err := d.GetState()
		if err != nil {
			log.L.Warnf("Daemon %s died somehow. Clean up its vestige!, %s", d.ID(), err)
			(*recoveringDaemons)[d.ID()] = d
			//nolint:nilerr
			return nil
		}

		if state != types.DaemonStateRunning {
			log.L.Warnf("daemon %s is not running: %s", d.ID(), state)
			return nil
		}

		// FIXME: Should put the a daemon back file system shared damon field.
		log.L.Infof("found RUNNING daemon %s during reconnecting", d.ID())
		(*liveDaemons)[d.ID()] = d

		if m.CgroupMgr != nil {
			if err := m.CgroupMgr.AddProc(d.States.ProcessID); err != nil {
				return errors.Wrapf(err, "add daemon %s to cgroup failed", d.ID())
			}
		}
		d.Lock()
		collector.NewDaemonInfoCollector(&d.Version, 1).Collect()
		d.Unlock()

		go func() {
			if err := daemon.WaitUntilSocketExisted(d.GetAPISock(), d.Pid()); err != nil {
				log.L.Errorf("Nydusd %s probably not started", d.ID())
				return
			}

			if err = m.SubscribeDaemonEvent(d); err != nil {
				log.L.Errorf("Nydusd %s probably not started", d.ID())
				return
			}

			// Snapshotter's lost the daemons' states after exit, refetch them.
			d.SendStates()
		}()

		return nil
	}); err != nil {
		return errors.Wrapf(err, "walk daemons to reconnect")
	}

	return nil
}
