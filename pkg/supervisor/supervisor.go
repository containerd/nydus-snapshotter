/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package supervisor

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"path/filepath"

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/pkg/errors"

	"golang.org/x/net/context"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sys/unix"
)

type StatesStorage interface {
	// Save state to storage space.
	Save([]byte)
	// Load state from storage space.
	Load() ([]byte, error)
	// Clean the previously saved state.
	Clean()
}

// Store daemon states in memory
type MemStatesStorage struct {
	data []byte
}

func newMemStatesStorage() *MemStatesStorage {
	return &MemStatesStorage{
		data: []byte{},
	}
}

func (mss *MemStatesStorage) Save(data []byte) {
	mss.data = make([]byte, len(data))
	copy(mss.data, data)
}

func (mss *MemStatesStorage) Load() ([]byte, error) {
	data := make([]byte, len(mss.data))
	copy(data, mss.data)
	return data, nil
}

func (mss *MemStatesStorage) Clean() {
	mss.data = []byte{}
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
	su.dataStorage.Save(data)
}

// Load resources kept by this supervisor
//  1. daemon runtime states
//  2. file descriptor
//
// Note: the resources should be not be consumed.
func (su *Supervisor) load() ([]byte, int, error) {
	su.mu.Lock()
	defer su.mu.Unlock()

	data, err := su.dataStorage.Load()
	if err != nil {
		return nil, 0, err
	}

	return data, su.fd, nil
}

func recv(uc *net.UnixConn) ([]byte, int, error) {
	data := make([]byte, 0)
	oob := make([]byte, 0)

	var dataBufLen = 1024 * 256 // Bytes

	// oobSpace is the size of the oob slice required to store for multiple FDs. Note
	// that unix.UnixRights appears to make the assumption that fd is always int32,
	// so sizeof(fd) = 4.
	// At most can accommodate 64 fds
	var oobSpace = unix.CmsgSpace(4) * 64

	for {
		dataBuf := make([]byte, dataBufLen)
		oobBuf := make([]byte, oobSpace)

		n, oobn, _, _, err := uc.ReadMsgUnix(dataBuf, oobBuf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, 0, errors.Wrap(err, "receive message")
		}
		if n == 0 {
			break // EOF
		}

		data = append(data, dataBuf[:n]...)
		oob = append(oob, oobBuf[:oobn]...)
	}

	scms, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, 0, errors.Wrap(err, "parse control message")
	}

	var fds []int
	if len(scms) == 0 {
		return nil, 0, fmt.Errorf("received no control file descriptor")
	}

	scm := scms[0]
	fds, err = unix.ParseUnixRights(&scm)
	if err != nil {
		return nil, 0, errors.Wrap(err, "extract file descriptors")
	}

	var fd int
	if len(fds) > 0 {
		fd = fds[0]
	} else {
		fd = -1
	}

	return data, fd, nil
}

func send(uc *net.UnixConn, data []byte, fd int) error {
	oob := syscall.UnixRights(fd)

	for len(data) > 0 || len(oob) > 0 {
		n, oobn, err := uc.WriteMsgUnix(data, oob, nil)
		if err != nil {
			return errors.Wrapf(err, "send message, datan %d oobn %d", n, oobn)
		}

		data = data[n:]
		oob = oob[oobn:]
	}

	return nil
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

		data, fd, err := recv(conn.(*net.UnixConn))
		if err != nil {
			return err
		}
		log.L.Infof("Supervisor %s receives states. data %d", su.id, len(data))

		su.save(data, fd)

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

		// FIXME: It's possible that sending states happens before storing state to the storage.
		data, fd, err := su.load()
		if err != nil {
			return errors.Wrapf(err, "load resources for %s", su.id)
		}
		if err := send(conn.(*net.UnixConn), data, fd); err != nil {
			return err
		}

		log.L.Infof("Supervisor %s sends states. data %d", su.id, len(data))

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

	eg := errgroup.Group{}
	eg.Go(func() error {
		err := trigger()
		return errors.Wrapf(err, "trigger on %s", su.Sock())
	})

	eg.Go(func() error {
		err := receiver()
		return errors.Wrapf(err, "receiver on %s", su.Sock())
	})

	// FIXME: With Timeout context!
	return eg.Wait()
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
