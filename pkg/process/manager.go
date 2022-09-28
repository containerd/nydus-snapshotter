/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package process

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/nydussdk"
	"github.com/containerd/nydus-snapshotter/pkg/store"
	"github.com/containerd/nydus-snapshotter/pkg/utils/mount"
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

	old, ok := s.idxByDaemonID[daemon.ID]

	// TODO: No need to retain all daemons in the slice,
	// just use the map indexed by DaemonID
	if ok {
		for i, d := range s.daemons {
			if d.ID == daemon.ID {
				s.daemons[i] = daemon
			}
		}
	} else {
		s.daemons = append(s.daemons, daemon)
	}

	if ok && old.SnapshotID != daemon.SnapshotID {
		log.L.Panicf("same daemon ID with different snapshot ID")
	}

	s.idxByDaemonID[daemon.ID] = daemon
	s.idxBySnapshotID[daemon.SnapshotID] = daemon

	if ok {
		return old
	}

	return nil
}

func (s *DaemonStates) removeUnlocked(d *daemon.Daemon) *daemon.Daemon {
	delete(s.idxBySnapshotID, d.SnapshotID)
	delete(s.idxByDaemonID, d.ID)

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

func (s *DaemonStates) RemoveBySnapshotID(id string) *daemon.Daemon {
	return s.GetBySnapshotID(id, func(d *daemon.Daemon) { s.removeUnlocked(d) })
}

func (s *DaemonStates) RecoverDaemonState(d *daemon.Daemon) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.L.Infof("Recovering snapshot ID %s daemon ID %s", d.SnapshotID, d.ID)

	s.daemons = append(s.daemons, d)
	s.idxBySnapshotID[d.SnapshotID] = d
	s.idxByDaemonID[d.ID] = d
}

func (s *DaemonStates) GetByDaemonID(id string, op func(d *daemon.Daemon)) *daemon.Daemon {
	var daemon *daemon.Daemon
	s.mu.Lock()
	defer s.mu.Unlock()
	daemon = s.idxByDaemonID[id]

	if daemon != nil && op != nil {
		op(daemon)
	} else if daemon == nil {
		log.L.Warnf("daemon daemon_id=%s is not found", id)
	}

	return daemon
}

func (s *DaemonStates) GetBySnapshotID(id string, op func(d *daemon.Daemon)) *daemon.Daemon {
	var daemon *daemon.Daemon
	s.mu.Lock()
	defer s.mu.Unlock()
	daemon = s.idxBySnapshotID[id]

	if daemon != nil && op != nil {
		op(daemon)
	} else if daemon == nil {
		log.L.Warnf("daemon snapshot_id=%s is not found", id)
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
	nydusdBinaryPath string
	daemonMode       string
	cacheDir         string
	// Daemon states are inserted when creating snapshots and nydusd and
	// removed when snapshot is deleted and nydusd is stopped. The persisted
	// daemon state should be updated respectively. For fetch daemon state, it
	// should never read a daemon from DB. Because the daemon states cache is
	// supposed to refilled when nydus-snapshotter restarting.
	daemonStates *DaemonStates

	mounter mount.Interface

	monitor LivenessMonitor
	// TODO: Close me
	LivenessNotifier chan deathEvent
	RecoverPolicy    DaemonRecoverPolicy

	// Protects updating states cache and DB
	mu sync.Mutex
}

type Opt struct {
	NydusdBinaryPath string
	Database         *store.Database
	DaemonMode       string
	CacheDir         string
	RecoverPolicy    DaemonRecoverPolicy
}

func (m *Manager) handleDaemonDeathEvent() {
	for ev := range m.LivenessNotifier {
		log.L.Warnf("Daemon %s died! socket path %s", ev.daemonID, ev.path)

		if m.RecoverPolicy == RecoverPolicyRestart {
			go func(event deathEvent) {
				d := m.GetByDaemonID(event.daemonID)
				if d == nil {
					log.L.Warnf("Daemon %s is not found", event.daemonID)
					return
				}

				if err := d.Wait(); err != nil {
					log.L.Warnf("fails to wait for daemon, %v", err)
				}

				// Starting a new nydusd will re-subscribe
				if err := m.monitor.Unsubscribe(d.ID); err != nil {
					log.L.Warnf("fails to unsubscribe daemon %s, %v", d.ID, err)
				}

				d.ClearVestige()
				if err := m.StartDaemon(d); err != nil {
					log.L.Errorf("fails to start daemon %s when recovering", d.ID)
					return
				}

				// Mount rafs instance by http API
				if d.IsSharedDaemon() {
					daemons := m.daemonStates.List()
					for _, d := range daemons {
						if d.ID != daemon.SharedNydusDaemonID {
							// FIXME: Virtual daemon has a separated client, so it skips checking unix socket's existence.
							// This is really hacky, but I don't have better solution until rafs object is decoupled from daemon.
							d.ResetClient()
							if err := d.SharedMount(); err != nil {
								log.L.Warnf("fail to mount rafs instance, %v", err)
							}
						}
					}
				}
			}(ev)
		} else if m.RecoverPolicy == RecoverPolicyFailover {
			log.L.Warnf("failover is not implemented now!")
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

	mgr := &Manager{
		store:            s,
		mounter:          &mount.Mounter{},
		nydusdBinaryPath: opt.NydusdBinaryPath,
		daemonMode:       opt.DaemonMode,
		cacheDir:         opt.CacheDir,
		daemonStates:     newDaemonStates(),
		monitor:          monitor,
		LivenessNotifier: make(chan deathEvent, 32),
		RecoverPolicy:    opt.RecoverPolicy,
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

	if old := m.daemonStates.GetByDaemonID(daemon.ID, nil); old != nil {
		return errdefs.ErrAlreadyExists
	}

	m.daemonStates.Add(daemon)
	return m.store.Add(daemon)
}

func (m *Manager) UpdateDaemon(daemon *daemon.Daemon) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if old := m.daemonStates.GetByDaemonID(daemon.ID, nil); old == nil {
		return errdefs.ErrNotFound
	}

	// Notice: updating daemon states cache and DB should be protect by `mu` lock
	m.daemonStates.Add(daemon)
	return m.store.Update(daemon)
}

func (m *Manager) DeleteBySnapshotID(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// FIXME: it will introduce deserialization.
	// We should not use pointer of daemon as KEY to operate DB.
	if d, err := m.store.GetBySnapshotID(id); err == nil {
		m.store.Delete(d)
	} else {
		log.L.Warnf("Failed to find daemon %s in DB", id)
	}

	m.daemonStates.RemoveBySnapshotID(id)
}

// Daemon state should always be fetched from states cache. DB is only
// the persistence storage. Daemons manager should never try to read
// serialized daemon state from DB when running normally. To the function
// does not try to read DB when daemon is not found.
func (m *Manager) GetBySnapshotID(id string) *daemon.Daemon {
	return m.daemonStates.GetBySnapshotID(id, nil)
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

	if err := m.store.Delete(daemon); err != nil {
		return errors.Wrapf(err, "delete daemon state for %s", daemon.ID)
	}

	m.daemonStates.Remove(daemon)

	return nil
}

func (m *Manager) ListDaemons() []*daemon.Daemon {
	return m.daemonStates.List()
}

func (m *Manager) CleanUpDaemonResources(d *daemon.Daemon) {
	resource := []string{d.ConfigDir, d.LogDir}
	if d.IsMultipleDaemon() {
		resource = append(resource, d.SocketDir)
	}

	for _, dir := range resource {
		if err := os.RemoveAll(dir); err != nil {
			log.L.Errorf("failed to remove dir %s err %v", dir, err)
		}
	}

	log.L.Infof("Deleting resources %v", resource)
}

func (m *Manager) StartDaemon(d *daemon.Daemon) error {
	cmd, err := m.buildStartCommand(d)
	if err != nil {
		return errors.Wrapf(err, "failed to create command for daemon %s", d.ID)
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	d.Lock()
	defer d.Unlock()

	d.Pid = cmd.Process.Pid

	// Update both states cache and DB
	// TODO: Is it right to commit daemon before nydusd successfully started?
	// And it brings extra latency of accessing DB. Only write daemon record to
	// DB when nydusd is started?
	err = m.UpdateDaemon(d)
	if err != nil {
		// Nothing we can do, just ignore it for now
		log.L.Errorf("fail to update daemon info (%+v) to DB: %v", d, err)
	}

	// If nydusd fails startup, manager can't subscribe its death event.
	// So we can ignore the subscribing error.
	go func() {
		if err := nydussdk.WaitUntilSocketExisted(d.GetAPISock()); err != nil {
			log.L.Errorf("Nydusd %s probably not started", d.ID)
			return
		}

		// TODO: It's better to subscribe death event when snapshotter
		// has set daemon's state to RUNNING or READY.
		if err := m.monitor.Subscribe(d.ID, d.GetAPISock(), m.LivenessNotifier); err != nil {
			log.L.Errorf("Nydusd %s probably not started", d.ID)
		}
	}()

	return nil
}

func (m *Manager) buildStartCommand(d *daemon.Daemon) (*exec.Cmd, error) {
	var args []string
	if d.FsDriver == config.FsDriverFscache {
		args = []string{
			"singleton",
			"--fscache", m.cacheDir,
		}
		nydusdThreadNum := d.NydusdThreadNum()
		if nydusdThreadNum != "" {
			args = append(args, "--fscache-threads", nydusdThreadNum)
		}
	} else {
		args = []string{"fuse"}
		nydusdThreadNum := d.NydusdThreadNum()
		if nydusdThreadNum != "" {
			args = append(args, "--thread-num", nydusdThreadNum)
		}

		switch {
		case d.IsMultipleDaemon():
			bootstrap, err := d.BootstrapFile()
			if err != nil {
				return nil, err
			}
			args = append(args,
				"--config",
				d.ConfigFile(),
				"--bootstrap",
				bootstrap,
				"--mountpoint",
				d.MountPoint(),
			)
		case m.isOneDaemon():
			args = append(args,
				"--mountpoint",
				*d.RootMountPoint,
			)
		default:
			return nil, errors.Errorf("DaemonMode %s doesn't have daemon configured", d.DaemonMode)
		}
	}

	args = append(args, "--apisock", d.GetAPISock())
	args = append(args, "--log-level", d.LogLevel)
	if !d.LogToStdout {
		args = append(args, "--log-file", d.LogFile())
	}

	log.L.Infof("start nydus daemon: %s %s", m.nydusdBinaryPath, strings.Join(args, " "))

	cmd := exec.Command(m.nydusdBinaryPath, args...)
	// nydusd standard output and standard error rather than its logs are
	// always redirected to snapshotter's respectively
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd, nil
}

func (m *Manager) DestroyBySnapshotID(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.daemonStates.GetBySnapshotID(id, nil)
	return m.DestroyDaemon(d)
}

// FIXME: should handle the inconsistent status caused by any step
// in the function that returns an error.
func (m *Manager) DestroyDaemon(d *daemon.Daemon) error {
	// Delete daemon from DB in the first place in case any of below steps fails
	// ending up with daemon is residual in DB.
	if err := m.DeleteDaemon(d); err != nil {
		return errors.Wrapf(err, "delete daemon %s", d.ID)
	}

	defer m.CleanUpDaemonResources(d)

	// if daemon is shared mount or use shared mount to do
	// prefetch, we should only umount the daemon with api instead
	// of umount entire mountpoint
	if m.isOneDaemon() {
		log.L.Infof("umount remote snapshot for shared daemon, mountpoint %s", d.SharedMountPoint())
		if err := d.SharedUmount(); err != nil {
			return errors.Wrap(err, "shared umount on destroying daemon")
		}
		return nil
	}

	if err := m.monitor.Unsubscribe(d.ID); err != nil {
		log.L.Warnf("Unable to unsubscribe, daemon ID %s", d.ID)
	}

	log.L.Infof("Destroy nydusd daemon %s. Host mountpoint %s, snapshot %s", d.ID, d.HostMountPoint(), d.SnapshotID)

	// Graceful nydusd termination will umount itself.
	if err := d.Terminate(); err != nil {
		log.L.Warnf("Fails to terminate daemon, %v", err)
	}

	if err := d.Wait(); err != nil {
		log.L.Warnf("Fails to wait for daemon, %v", err)
	}

	return nil
}

func (m *Manager) isOneDaemon() bool {
	return m.daemonMode == config.DaemonModeShared ||
		m.daemonMode == config.DaemonModePrefetch
}

func (m *Manager) isNoneDaemon() bool {
	return m.daemonMode == config.DaemonModeNone
}

func (m *Manager) IsSharedDaemon() bool {
	return m.daemonMode == config.DaemonModeShared
}

func (m *Manager) IsPrefetchDaemon() bool {
	return m.daemonMode == config.DaemonModePrefetch
}

// Reconnect running daemons and rebuild daemons management states
// 1. Don't erase ever written record
// 2. Just recover nydusd daemon states to manager's memory part.
// 3. Manager in SharedDaemon mode should starts a nydusd when recovering
func (m *Manager) Reconnect(ctx context.Context) ([]*daemon.Daemon, error) {
	var (
		sharedDaemon *daemon.Daemon
		// Collected deserialized daemons that need to be recovered.
		recoveringDaemons []*daemon.Daemon
	)

	if m.isNoneDaemon() {
		return nil, nil
	}

	if err := m.store.WalkDaemons(ctx, func(d *daemon.Daemon) error {
		log.L.WithField("daemon", d.ID).
			WithField("mode", d.DaemonMode).
			Info("found daemon in database")

		m.daemonStates.RecoverDaemonState(d)

		d.ResetClient()

		defer func() {
			// `CheckStatus->ensureClient` only checks if socket file is existed when building http client.
			// But the socket file may be residual and will be cleared before starting a new nydusd.
			// So clear the client by assigning nil
			d.ResetClient()
		}()

		// Do not check status on virtual daemons
		if m.isOneDaemon() && d.ID != daemon.SharedNydusDaemonID {
			log.L.WithField("daemon", d.ID).Infof("found virtual daemon")
			recoveringDaemons = append(recoveringDaemons, d)

			// It a coincidence that virtual daemon has the same socket path with shared daemon.
			// Check if nydusd can be connected before clear their vestiges.
			_, err := d.CheckStatus()
			if err != nil {
				log.L.WithField("daemon", d.ID).Warnf("failed to check daemon status: %v", err)

				if d.FsDriver == config.FsDriverFscache {
					d.ClearVestige()
				}
			}

			return nil
		}

		info, err := d.CheckStatus()
		if err != nil {
			log.L.WithField("daemon", d.ID).Warnf("failed to check daemon status: %v", err)

			// Skip so-called virtual daemon :-(
			if d.ID == daemon.SharedNydusDaemonID || !m.isOneDaemon() && d.ID != daemon.SharedNydusDaemonID {
				// The only reason that nydusd can't be connected is it's not running.
				// Moreover, snapshotter is restarting. So no nydusd states can be returned to each nydusd.
				// Nydusd can't do failover any more.
				// We can safely try to umount its mountpoint to avoid nydusd pausing in INIT state.
				log.L.Warnf("Nydusd died somehow. Clean up its vestige!")
				d.ClearVestige()
			}

			recoveringDaemons = append(recoveringDaemons, d)
			return nil
		}

		d.Connected = true

		if !info.Running() {
			log.L.WithField("daemon", d.ID).Warnf("daemon is not running: %v", info)
			return nil
		}

		log.L.WithField("daemon", d.ID).Infof("found alive daemon")

		go func() {
			if err := nydussdk.WaitUntilSocketExisted(d.GetAPISock()); err != nil {
				log.L.Errorf("Nydusd %s probably not started", d.ID)
				return
			}

			if err := m.monitor.Subscribe(d.ID, d.GetAPISock(), m.LivenessNotifier); err != nil {
				log.L.Errorf("Nydusd %s probably not started", d.ID)
			}
		}()

		// Get the global shared daemon here after CheckStatus() by attention
		// so that we're sure it's alive.
		if d.ID == daemon.SharedNydusDaemonID {
			sharedDaemon = d
		}

		return nil
	}); err != nil {
		return nil, errors.Wrapf(err, "failed to walk daemons to reconnect")
	}

	if !m.isOneDaemon() && sharedDaemon != nil {
		return nil, errors.Errorf("SharedDaemon or PrefetchDaemon disabled, but shared daemon is found")
	}

	return recoveringDaemons, nil
}
