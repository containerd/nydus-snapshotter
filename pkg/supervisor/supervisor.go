/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package supervisor

import (
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"path/filepath"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/pkg/errors"

	"golang.org/x/net/context"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sys/unix"
)

const MaxOpaqueLen = 1024 * 32 // Bytes

// oobSpace is the size of the oob slice required to store for multiple FDs. Note
// that unix.UnixRights appears to make the assumption that fd is always int32,
// so sizeof(fd) = 4.
// At most can accommodate 64 fds
var oobSpace = unix.CmsgSpace(4) * 64

type StatesStorage interface {
	// Appended write states to the storage space.
	Write([]byte)
	// Read out the previously written states to fill `buf` which should be large enough.
	Read(buf []byte) (uint, error)
	// Mark all data as stale, the previously written data is cleaned
	Clean()
}

// Store daemon states in memory
type MemStatesStorage struct {
	data []byte
	head int
}

func newMemStatesStorage() *MemStatesStorage {
	return &MemStatesStorage{data: make([]byte, MaxOpaqueLen)}
}

func (mss *MemStatesStorage) Write(data []byte) {
	l := copy(mss.data[mss.head:], data)
	mss.head += l
}

func (mss *MemStatesStorage) Read(data []byte) (uint, error) {
	l := copy(data, mss.data[:mss.head])
	return uint(l), nil
}

func (mss *MemStatesStorage) Clean() {
	mss.head = 0
}

// Use daemon ID as the supervisor ID
type Supervisor struct {
	id string
	// To which nydusd daemon will try to connect
	path string
	// Hold the sended file descriptors.
	fd          int
	dataStorage StatesStorage
	mu          sync.Mutex
	sem         *semaphore.Weighted
}

func (su *Supervisor) save(data []byte, fd int) {
	su.mu.Lock()
	defer su.mu.Unlock()

	// Always overwrite states and FDs
	// We should clean up the stored states since each received states set is atomic
	su.dataStorage.Clean()
	if fd > 0 {
		su.fd = fd
	}
	su.dataStorage.Write(data)
}

// Load resources kept by this supervisor
//  1. daemon runtime states
//  2. file descriptor
//
// Note: the resources should be not be consumed.
func (su *Supervisor) load(data []byte, oob []byte) (nData uint, nOob int, err error) {
	su.mu.Lock()
	defer su.mu.Unlock()

	if su.fd > 0 {
		b := syscall.UnixRights(su.fd)
		nOob = copy(oob, b)
	}

	nData, err = su.dataStorage.Read(data)
	if err != nil {
		return 0, 0, err
	}

	return nData, nOob, nil
}

// There are several stages from different goroutines to trigger sending daemon states
// the waiter will overlap each other causing the UDS being deleted.
// But we don't want to keep the server listen for ever.
// `to` equal to zero indicates that caller should call the receiver the receive stats
// when it thinks is appropriate. `to` is not zero, no receiver callback will be returned.
// Then this method is responsible to receive states inside.
func (su *Supervisor) waitStatesTimeout(to time.Duration) (func() error, error) {
	if err := os.Remove(su.path); err != nil {
		if !os.IsNotExist(err) {
			log.L.Warnf("Unable to remove existed socket file %s, %s", su.path, err)
		}
	}

	listener, err := net.Listen("unix", su.path)
	if err != nil {
		return nil, errors.Wrapf(err, "listen on socket %s", su.path)
	}

	receiver := func() error {
		defer listener.Close()

		// After the listener is closed, Accept() wakes up
		conn, err := listener.Accept()
		if err != nil {
			return errors.Wrapf(err, "Listener is closed")
		}

		defer conn.Close()

		unixConn := conn.(*net.UnixConn)
		uf, err := unixConn.File()
		if err != nil {
			return err
		}

		defer uf.Close()

		data := make([]byte, MaxOpaqueLen)
		oob := make([]byte, oobSpace) // Out-of-band data

		// TODO: Handle EAGAIN  EOF and EINTR
		n, oobn, _, _, err := unix.Recvmsg(int(uf.Fd()), data, oob, 0)
		if err != nil {
			return errors.Wrap(err, "receive message")
		}

		log.L.Infof("Supervisor %s receives states. data %d oob %d", su.id, n, oobn)

		scms, err := unix.ParseSocketControlMessage(oob[:oobn])
		if err != nil {
			return errors.Wrap(err, "parse control message")
		}

		var fds []int
		if len(scms) > 0 {
			scm := scms[0]
			fds, err = unix.ParseUnixRights(&scm)
			if err != nil {
				return errors.Wrap(err, "extract file descriptors")
			}
		} else {
			log.L.Warn("received no control file descriptor")
		}

		var fd int
		if len(fds) > 0 {
			fd = fds[0]
		} else {
			fd = -1
		}

		su.save(data[:n], fd)
		return nil
	}

	cancelTimer := make(chan int, 1)
	// Once timeouts, stop waiting for states
	if to > 0 {
		timer := time.NewTimer(to)
		go func() {
			select {
			case <-timer.C:
				log.L.Warnf("Receiving state timeouts after %s", to)
				// Wake up the blocking `Accept`
				listener.Close()
			case <-cancelTimer:
			}

		}()

		go func() {
			if err := receiver(); err != nil {
				log.L.Errorf("receiver fails, %s", err)
			}
			if to > 0 {
				cancelTimer <- 1
			}
		}()

		// With non-zero timeout parameter, call should be aware that receiver is nil
		//nolint: nilnil
		return nil, nil
	}

	return receiver, nil
}

func (su *Supervisor) SendStatesTimeout(to time.Duration) error {
	// It is used to receive before
	if err := os.Remove(su.path); err != nil {
		if !os.IsNotExist(err) {
			log.L.Warnf("Unable to remove existed socket file %s, %s", su.path, err)
		}
	}

	listener, err := net.Listen("unix", su.path)
	if err != nil {
		return errors.Wrap(err, "listen on socket")
	}

	sender := func() error {
		defer listener.Close()

		conn, err := listener.Accept()
		if err != nil {
			return errors.Wrapf(err, "Listener is closed")
		}

		defer conn.Close()

		unixConn := conn.(*net.UnixConn)
		uf, err := unixConn.File()
		if err != nil {
			return err
		}
		defer uf.Close()

		data := make([]byte, MaxOpaqueLen)
		oob := make([]byte, oobSpace)

		// FIXME: It's possible that sending states happens before storing state to the storage.

		datan, oobn, err := su.load(data, oob)
		if err != nil {
			return errors.Wrapf(err, "load resources for %s", su.id)
		}
		// TODO: validate returned length
		_, _, err = unixConn.WriteMsgUnix(data[:datan], oob[:oobn], nil)
		if err != nil {
			return errors.Wrapf(err, "send message, datan %d oobn %d", datan, oobn)
		}

		log.L.Infof("Supervisor %s sends states. data %d oob %d", su.id, datan, oobn)

		return nil
	}

	cancelTimer := make(chan int, 1)
	if to > 0 {
		timer := time.NewTimer(to)
		go func() {
			select {
			case <-timer.C:
				log.L.Warnf("Sending state timeouts after %s", to)
				// Wake up the blocking `Accept()`
				listener.Close()
			case <-cancelTimer:
			}

		}()
	}

	// Once timeouts, stop waiting for others fetching states
	go func() {
		err := sender()
		if err != nil {
			log.L.Errorf("Sender fails, %s", err)
		}
		if to > 0 {
			cancelTimer <- 1
		}
	}()

	return nil
}

func (su *Supervisor) FetchDaemonStates(trigger func() error) error {
	if err := su.sem.Acquire(context.TODO(), 1); err != nil {
		return err
	}

	defer su.sem.Release(1)

	receiver, err := su.waitStatesTimeout(0)
	if err != nil {
		return errors.Wrapf(err, "wait states on %s", su.Sock())
	}

	err = trigger()
	if err != nil {
		return errors.Wrapf(err, "trigger on %s", su.Sock())
	}

	// FIXME: With Timeout context!
	return receiver()
}

// The unix domain socket on which nydus daemon is connected to
func (su *Supervisor) Sock() string {
	return su.path
}

// Manage all supervisors each of which works for a nydusd to keep its resources
// for sake of failover and live-upgrade.
type SupervisorsSet struct {
	mu  sync.Mutex
	set map[string]*Supervisor
	// A directory where all the supervisor sockets resides.
	root string
}

func NewSupervisorSet(root string) (*SupervisorsSet, error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}

	return &SupervisorsSet{
		set:  make(map[string]*Supervisor),
		root: root}, nil
}

func (ss *SupervisorsSet) NewSupervisor(id string) *Supervisor {
	sockPath := filepath.Join(ss.root, fmt.Sprintf("%s.sock", id))

	supervisor := &Supervisor{
		id:   id,
		path: sockPath,
		// Negative value means no FD was ever held.
		fd:          -1,
		dataStorage: newMemStatesStorage(),
		sem:         semaphore.NewWeighted(1),
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()

	// Allow overwrite the old supervisor
	ss.set[id] = supervisor

	return supervisor
}

// Get supervisor by its id which is typically the nydus damon ID.
func (ss *SupervisorsSet) GetSupervisor(id string) *Supervisor {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	return ss.set[id]
}

func (ss *SupervisorsSet) DestroySupervisor(id string) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	supervisor, ok := ss.set[id]
	if !ok {
		return errdefs.ErrNotFound
	}

	delete(ss.set, id)

	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()

	if supervisor.fd > 0 {
		// Prevent hanging after nydusd exits.
		if err := syscall.Close(supervisor.fd); err != nil {
			log.L.Errorf("Fail to close fd %d, %s", supervisor.fd, err)
		}
	}

	return nil
}
