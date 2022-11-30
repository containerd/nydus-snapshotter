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
	"time"

	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/internal/config"
	"github.com/containerd/nydus-snapshotter/internal/containerd-nydus-grpc/command"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/collector"
	"github.com/containerd/nydus-snapshotter/pkg/store"
	"github.com/containerd/nydus-snapshotter/pkg/supervisor"
)

type DaemonRecoverPolicy int

const (
	RecoverPolicyInvalid DaemonRecoverPolicy = iota
	RecoverPolicyNone
	RecoverPolicyRestart
	RecoverPolicyFailover
)

func (p DaemonRecoverPolicy) String() string {
	switch p {
	case RecoverPolicyNone:
		return "none"
	case RecoverPolicyRestart:
		return "restart"
	case RecoverPolicyFailover:
		return "failover"
	case RecoverPolicyInvalid:
		fallthrough
	default:
		return ""
	}
}

var recoverPolicyParser map[string]DaemonRecoverPolicy

func init() {
	recoverPolicyParser = map[string]DaemonRecoverPolicy{
		RecoverPolicyNone.String():     RecoverPolicyNone,
		RecoverPolicyRestart.String():  RecoverPolicyRestart,
		RecoverPolicyFailover.String(): RecoverPolicyFailover}
}

func ParseRecoverPolicy(p string) (DaemonRecoverPolicy, error) {
	policy, ok := recoverPolicyParser[p]
	if !ok {
		return RecoverPolicyInvalid, errdefs.ErrNotFound
	}

	return policy, nil
}

type DaemonStates struct {
	mu              sync.Mutex
	idxBySnapshotID map[string]*daemon.Daemon // index by snapshot ID
	idxByDaemonID   map[string]*daemon.Daemon // index by ID
	daemons         []*daemon.Daemon          // all daemon
}

func newDaemonStates() *DaemonStates {
	return &DaemonStates{
		idxBySnapshotID: make(map[string]*daemon.Daemon),
		idxByDaemonID:   make(map[string]*daemon.Daemon),
	}
}

// Return nil if the daemon is never inserted or managed,
// otherwise returns the previously inserted daemon pointer.
// Allowing replace an existed daemon since some fields in Daemon can change after restarting nydusd.
func (s *DaemonStates) Add(daemon *daemon.Daemon) *daemon.Daemon {
	s.mu.Lock()
	defer s.mu.Unlock()

	old, ok := s.idxByDaemonID[daemon.ID()]

	// TODO: No need to retain all daemons in the slice,
	// just use the map indexed by DaemonID
	if ok {
		for i, d := range s.daemons {
			if d.ID() == daemon.ID() {
				s.daemons[i] = daemon
			}
		}
	} else {
		s.daemons = append(s.daemons, daemon)
	}

	s.idxByDaemonID[daemon.ID()] = daemon

	if ok {
		return old
	}

	return nil
}

func (s *DaemonStates) removeUnlocked(d *daemon.Daemon) *daemon.Daemon {
	delete(s.idxByDaemonID, d.ID())

	var deleted *daemon.Daemon

	ds := s.daemons[:0]
	for _, remained := range s.daemons {
		if remained == d {
			deleted = remained
			continue
		}
		ds = append(ds, remained)
	}

	s.daemons = ds

	return deleted
}

func (s *DaemonStates) Remove(d *daemon.Daemon) *daemon.Daemon {
	s.mu.Lock()
	old := s.removeUnlocked(d)
	s.mu.Unlock()

	return old
}

func (s *DaemonStates) RemoveByDaemonID(id string) *daemon.Daemon {
	return s.GetByDaemonID(id, func(d *daemon.Daemon) { s.removeUnlocked(d) })
}

// Also recover daemon runtime state here
func (s *DaemonStates) RecoverDaemonState(d *daemon.Daemon) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.L.Infof("Recovering daemon ID %s", d.ID())

	s.daemons = append(s.daemons, d)
	s.idxByDaemonID[d.ID()] = d
}

func (s *DaemonStates) GetByDaemonID(id string, op func(d *daemon.Daemon)) *daemon.Daemon {
	var daemon *daemon.Daemon
	s.mu.Lock()
	defer s.mu.Unlock()
	daemon = s.idxByDaemonID[id]

	if daemon != nil && op != nil {
		op(daemon)
	}

	return daemon
}

func (s *DaemonStates) List() []*daemon.Daemon {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.daemons) == 0 {
		return nil
	}

	listed := make([]*daemon.Daemon, len(s.daemons))
	copy(listed, s.daemons)

	return listed
}

func (s *DaemonStates) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.daemons)
}

// Manage all nydusd daemons. Provide a daemon states cache
// to avoid frequently operating DB
type Manager struct {
	store            Store
	NydusdBinaryPath string
	daemonMode       command.DaemonMode
	// Where nydusd stores cache files for fscache driver
	cacheDir string
	// Daemon states are inserted when creating snapshots and nydusd and
	// removed when snapshot is deleted and nydusd is stopped. The persisted
	// daemon state should be updated respectively. For fetch daemon state, it
	// should never read a daemon from DB. Because the daemon states cache is
	// supposed to refilled when nydus-snapshotter restarting.
	daemonStates *DaemonStates

	monitor LivenessMonitor
	// TODO: Close me
	LivenessNotifier chan deathEvent
	RecoverPolicy    DaemonRecoverPolicy
	SupervisorSet    *supervisor.SupervisorsSet

	// A basic configuration template loaded from the file
	DaemonConfig config.DaemonConfigInterface

	// In order to validate daemon fs driver is consistent with the latest snapshotter boot
	FsDriver string

	// Protects updating states cache and DB
	mu sync.Mutex
}

type Opt struct {
	NydusdBinaryPath string
	Database         *store.Database
	DaemonMode       command.DaemonMode
	CacheDir         string
	RecoverPolicy    DaemonRecoverPolicy
	// Nydus-snapshotter work directory
	RootDir      string
	DaemonConfig config.DaemonConfigInterface
	// In order to validate daemon fs driver is consistent with the latest snapshotter boot
	FsDriver string
}

func (m *Manager) doDaemonFailover(d *daemon.Daemon) {
	if err := d.Wait(); err != nil {
		log.L.Warnf("fail to wait for daemon, %v", err)
	}

	// Starting a new nydusd will re-subscribe
	if err := m.monitor.Unsubscribe(d.ID()); err != nil {
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
		log.L.Errorf("daemon din't reach state %s", types.DaemonStateInit)
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
	if err := m.monitor.Unsubscribe(d.ID()); err != nil {
		log.L.Warnf("fails to unsubscribe daemon %s, %v", d.ID(), err)
	}

	d.ClearVestige()
	if err := m.StartDaemon(d); err != nil {
		log.L.Errorf("fails to start daemon %s when recovering", d.ID())
		return
	}

	// Mount rafs instance by http API
	instances := d.Instances.List()
	for _, r := range instances {
		// Rafs is already mounted during starting nydusd
		if d.HostMountpoint() == r.GetMountpoint() {
			break
		}

		if err := d.SharedMount(r); err != nil {
			log.L.Warnf("Failed to mount rafs instance, %v", err)
		}
	}
}

func (m *Manager) handleDaemonDeathEvent() {
	for ev := range m.LivenessNotifier {
		log.L.Warnf("Daemon %s died! socket path %s", ev.daemonID, ev.path)

		d := m.GetByDaemonID(ev.daemonID)
		if d == nil {
			log.L.Warnf("Daemon %s was not found", ev.daemonID)
			return
		}

		d.State = types.DaemonStateUnknown
		if m.RecoverPolicy == RecoverPolicyRestart {
			log.L.Infof("Restart daemon %s", ev.daemonID)
			go m.doDaemonRestart(d)
		} else if m.RecoverPolicy == RecoverPolicyFailover {
			log.L.Infof("Do failover for daemon %s", ev.daemonID)
			go m.doDaemonFailover(d)
		}
	}
}

func NewManager(opt Opt) (*Manager, error) {
	s, err := store.NewDaemonStore(opt.Database)
	if err != nil {
		return nil, err
	}

	monitor, err := newMonitor()
	if err != nil {
		return nil, errors.Wrap(err, "create daemons liveness monitor")
	}

	var supervisorSet *supervisor.SupervisorsSet
	if opt.RecoverPolicy == RecoverPolicyFailover {
		supervisorSet, err = supervisor.NewSupervisorSet(filepath.Join(opt.RootDir, "supervisor"))
		if err != nil {
			return nil, errors.Wrap(err, "create supervisor set")
		}
	}

	mgr := &Manager{
		store:            s,
		NydusdBinaryPath: opt.NydusdBinaryPath,
		daemonMode:       opt.DaemonMode,
		cacheDir:         opt.CacheDir,
		daemonStates:     newDaemonStates(),
		monitor:          monitor,
		LivenessNotifier: make(chan deathEvent, 32),
		RecoverPolicy:    opt.RecoverPolicy,
		SupervisorSet:    supervisorSet,
		DaemonConfig:     opt.DaemonConfig,
		FsDriver:         opt.FsDriver,
	}

	// FIXME: How to get error if monitor goroutine terminates with error?
	// TODO: Shutdown monitor immediately after snapshotter receive Exit signal
	mgr.monitor.Run()
	go mgr.handleDaemonDeathEvent()

	return mgr, nil
}

// Put a instantiated daemon into states manager. The damon state is
// put to both states cache and DB. If the daemon with the same
// daemon ID is already stored, return error ErrAlreadyExists
func (m *Manager) NewDaemon(daemon *daemon.Daemon) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if old := m.daemonStates.GetByDaemonID(daemon.ID(), nil); old != nil {
		return errdefs.ErrAlreadyExists
	}

	m.daemonStates.Add(daemon)
	return m.store.AddDaemon(daemon)
}

func (m *Manager) NewInstance(r *daemon.Rafs) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	seq, err := m.store.NextInstanceSeq()
	if err != nil {
		return err
	}

	r.Seq = seq

	return m.store.AddInstance(r)
}

func (m *Manager) Lock() {
	m.mu.Lock()
}

func (m *Manager) Unlock() {
	m.mu.Unlock()
}

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

func (m *Manager) RemoveInstance(snapshotID string) error {
	return m.store.DeleteInstance(snapshotID)
}

func (m *Manager) UpdateDaemon(daemon *daemon.Daemon) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if old := m.daemonStates.GetByDaemonID(daemon.ID(), nil); old == nil {
		return errdefs.ErrNotFound
	}

	// Notice: updating daemon states cache and DB should be protect by `mu` lock
	m.daemonStates.Add(daemon)
	return m.store.UpdateDaemon(daemon)
}

func (m *Manager) GetByDaemonID(id string) *daemon.Daemon {
	return m.daemonStates.GetByDaemonID(id, nil)
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

	m.daemonStates.Remove(daemon)

	return nil
}

func (m *Manager) ListDaemons() []*daemon.Daemon {
	return m.daemonStates.List()
}

func (m *Manager) CleanUpDaemonResources(d *daemon.Daemon) {
	resource := []string{d.States.ConfigDir, d.States.LogDir}
	if !m.IsSharedDaemon() {
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

// FIXME: should handle the inconsistent status caused by any step
// in the function that returns an error.
func (m *Manager) DestroyDaemon(d *daemon.Daemon) error {
	log.L.Infof("Destroy nydusd daemon %s. Host mountpoint %s", d.ID(), d.HostMountpoint())

	// Delete daemon from DB in the first place in case any of below steps fails
	// ending up with daemon is residual in DB.
	if err := m.DeleteDaemon(d); err != nil {
		return errors.Wrapf(err, "delete daemon %s", d.ID())
	}

	defer m.CleanUpDaemonResources(d)

	if err := d.UmountAllInstances(); err != nil {
		log.L.Errorf("Failed to detach all fs instances from daemon %s, %s", d.ID(), err)
	}

	if err := m.monitor.Unsubscribe(d.ID()); err != nil {
		log.L.Warnf("Unable to unsubscribe, daemon ID %s", d.ID())
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

	collector.CollectDaemonEvent(d.ID(), string(types.DaemonStateDestroyed))

	return nil
}

// Recover running daemons and rebuild daemons management states
// It is invoked during nydus-snapshotter restarting
// 1. Don't erase ever written record
// 2. Just recover nydusd daemon states to manager's memory part.
// 3. Manager in SharedDaemon mode should starts a nydusd when recovering
func (m *Manager) Recover(ctx context.Context) (map[string]*daemon.Daemon, map[string]*daemon.Daemon, error) {
	// Collected deserialized daemons that need to be recovered.
	recoveringDaemons := make(map[string]*daemon.Daemon, 0)
	liveDaemons := make(map[string]*daemon.Daemon, 0)

	if err := m.store.WalkDaemons(ctx, func(s *daemon.States) error {
		log.L.Debugf("found daemon states %#v", s)

		opt := make([]daemon.NewDaemonOpt, 0)
		var d, _ = daemon.NewDaemon(opt...)
		d.States = *s

		// It can't change snapshotter's fs driver to a different one from a daemon that ever created in the past.
		if d.States.FsDriver != m.FsDriver {
			return errors.Wrapf(errdefs.ErrInvalidArgument,
				"can't recover from the last restart, the specified fs-driver=%s mismatches with the last fs-driver=%s",
				d.States.FsDriver, m.FsDriver)
		}

		m.daemonStates.RecoverDaemonState(d)

		if m.SupervisorSet != nil {
			su := m.SupervisorSet.NewSupervisor(d.ID())
			if su == nil {
				return errors.Errorf("create supervisor for daemon %s", d.ID())
			}
			d.Supervisor = su
		}

		if d.States.FsDriver == command.FsDriverFusedev {
			cfg, err := config.NewDaemonConfig(d.States.FsDriver, d.ConfigFile(""))
			if err != nil {
				log.L.Errorf("Failed to reload daemon configuration %s, %s", d.ConfigFile(""), err)
				return err
			}

			d.Config = cfg
		}

		state, err := d.GetState()
		if err != nil {
			log.L.Warnf("Daemon %s died somehow. Clean up its vestige!, %s", d.ID(), err)
			recoveringDaemons[d.ID()] = d
			//nolint:nilerr
			return nil
		}

		if state != types.DaemonStateRunning {
			log.L.Warnf("daemon %s is not running: %s", d.ID(), state)
			return nil
		}

		// FIXME: Should put the a daemon back file system shared damon field.
		log.L.Infof("found RUNNING daemon %s during reconnecting", d.ID())
		liveDaemons[d.ID()] = d

		go func() {
			if err := daemon.WaitUntilSocketExisted(d.GetAPISock()); err != nil {
				log.L.Errorf("Nydusd %s probably not started", d.ID())
				return
			}

			if err := m.monitor.Subscribe(d.ID(), d.GetAPISock(), m.LivenessNotifier); err != nil {
				log.L.Errorf("Nydusd %s probably not started", d.ID())
				return
			}

			// Snapshotter's lost the daemons' states after exit, refetch them.
			su := d.Supervisor
			if su != nil {
				err = su.FetchDaemonStates(func() error {
					if err := d.SendStates(); err != nil {
						return errors.Wrapf(err, "send daemon %s states", d.ID())
					}
					return nil
				})
				if err != nil {
					log.L.Errorf("Send daemon %s states", d.ID())
					return
				}
			}
		}()

		return nil
	}); err != nil {
		return nil, nil, errors.Wrapf(err, "walk daemons to reconnect")
	}

	if err := m.store.WalkInstances(ctx, func(r *daemon.Rafs) error {
		log.L.Debugf("found instance %#v", r)

		d := recoveringDaemons[r.DaemonID]
		if d != nil {
			d.AddInstance(r)
		}

		d = liveDaemons[r.DaemonID]
		if d != nil {
			d.AddInstance(r)
		}

		daemon.RafsSet.Add(r)

		return nil
	}); err != nil {
		return nil, nil, errors.Wrapf(err, "walk instances to reconnect")
	}

	for _, d := range recoveringDaemons {
		d.ClearVestige()
	}

	return recoveringDaemons, liveDaemons, nil
}
