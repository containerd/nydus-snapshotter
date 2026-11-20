/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package supervisor

import (
	"context"
	"crypto/rand"
	"net"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSupervisor(t *testing.T) {
	rootDir, err1 := os.MkdirTemp("", "supervisor")
	assert.Nil(t, err1)

	t.Cleanup(func() {
		os.RemoveAll(rootDir)
	})

	supervisorSet, err := NewSupervisorSet(rootDir)
	assert.Nil(t, err)

	su1 := supervisorSet.NewSupervisor("su1")
	assert.NotNil(t, su1)
	defer func() {
		err = supervisorSet.DestroySupervisor("su1")
		assert.NotNil(t, su1)
	}()

	sock := su1.Sock()
	addr, err := net.ResolveUnixAddr("unix", sock)
	assert.Nil(t, err)

	// Build a large data to test the multiple recvmsg / sendmsg
	// syscalls can handle all the data.
	sentData := make([]byte, 1024*1024*2)
	_, err = rand.Read(sentData)
	assert.Nil(t, err)

	tmpFile, err := os.CreateTemp("", "nydus-supervisor-test")
	assert.Nil(t, err)
	defer tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	nydusdSendFd := func() error {
		conn, err := net.DialUnix("unix", nil, addr)
		assert.Nil(t, err)
		defer func() { _ = conn.Close() }()

		err = send(conn, sentData, int(tmpFile.Fd()))
		assert.Nil(t, err)

		return nil
	}

	err = su1.FetchDaemonStates(nydusdSendFd)
	assert.NoError(t, err)

	nydusdTakeover := func() {
		err = su1.SendStatesTimeout(0)
		assert.Nil(t, err)

		conn, err := net.DialUnix("unix", nil, addr)
		assert.Nil(t, err)

		recvData, _, err := recv(conn)
		assert.Nil(t, err)

		assert.Equal(t, len(sentData), len(recvData))
		assert.True(t, reflect.DeepEqual(recvData, sentData))
	}

	nydusdTakeover()
}

func TestSupervisorTimeout(t *testing.T) {
	rootDir, err1 := os.MkdirTemp("", "supervisor")
	assert.Nil(t, err1)

	t.Cleanup(func() {
		os.RemoveAll(rootDir)
	})

	supervisorSet, err := NewSupervisorSet(rootDir)
	assert.Nil(t, err, "%v", err)

	su1 := supervisorSet.NewSupervisor("su1")
	assert.NotNil(t, su1)

	err = su1.SendStatesTimeout(10 * time.Millisecond)
	assert.Nil(t, err, "%v", err)
	sock := su1.Sock()

	time.Sleep(200 * time.Millisecond)

	addr, err := net.ResolveUnixAddr("unix", sock)
	assert.Nil(t, err)

	_, err = net.DialUnix("unix", nil, addr)
	assert.NotNil(t, err, "%v", err)
}

func TestFetchDaemonStatesReturnsOnTriggerError(t *testing.T) {
	rootDir := t.TempDir()
	oldTimeout := fetchDaemonStatesTimeout
	fetchDaemonStatesTimeout = 100 * time.Millisecond
	t.Cleanup(func() {
		fetchDaemonStatesTimeout = oldTimeout
	})

	supervisorSet, err := NewSupervisorSet(rootDir)
	assert.NoError(t, err)
	su := supervisorSet.NewSupervisor("su1")
	assert.NotNil(t, su)

	start := time.Now()
	err = su.FetchDaemonStates(func() error {
		return assert.AnError
	})
	assert.ErrorIs(t, err, assert.AnError)
	assert.Less(t, time.Since(start), time.Second)
}

func TestFetchDaemonStatesTimesOut(t *testing.T) {
	rootDir := t.TempDir()
	oldTimeout := fetchDaemonStatesTimeout
	fetchDaemonStatesTimeout = 100 * time.Millisecond
	t.Cleanup(func() {
		fetchDaemonStatesTimeout = oldTimeout
	})

	supervisorSet, err := NewSupervisorSet(rootDir)
	assert.NoError(t, err)
	su := supervisorSet.NewSupervisor("su1")
	assert.NotNil(t, su)

	start := time.Now()
	err = su.FetchDaemonStates(func() error {
		return nil
	})
	assert.Error(t, err)
	assert.Less(t, time.Since(start), time.Second)
}

func TestFetchDaemonStatesSkipsWhenSemaphoreBusy(t *testing.T) {
	rootDir := t.TempDir()
	oldAcquireTimeout := fetchDaemonStatesAcquireTimeout
	fetchDaemonStatesAcquireTimeout = 100 * time.Millisecond
	t.Cleanup(func() {
		fetchDaemonStatesAcquireTimeout = oldAcquireTimeout
	})

	supervisorSet, err := NewSupervisorSet(rootDir)
	assert.NoError(t, err)
	su := supervisorSet.NewSupervisor("su1")
	assert.NotNil(t, su)

	err = su.sem.Acquire(context.Background(), 1)
	assert.NoError(t, err)
	defer su.sem.Release(1)

	start := time.Now()
	err = su.FetchDaemonStates(func() error {
		t.Fatal("trigger should not run when semaphore acquisition times out")
		return nil
	})
	assert.ErrorIs(t, err, ErrFetchDaemonStatesSkipped)
	assert.Less(t, time.Since(start), time.Second)
}

func TestSendStatesTimeoutBlocksFetchDaemonStates(t *testing.T) {
	rootDir := t.TempDir()
	oldAcquireTimeout := fetchDaemonStatesAcquireTimeout
	fetchDaemonStatesAcquireTimeout = 100 * time.Millisecond
	t.Cleanup(func() {
		fetchDaemonStatesAcquireTimeout = oldAcquireTimeout
	})

	supervisorSet, err := NewSupervisorSet(rootDir)
	assert.NoError(t, err)
	su := supervisorSet.NewSupervisor("su1")
	assert.NotNil(t, su)

	err = su.SendStatesTimeout(200 * time.Millisecond)
	assert.NoError(t, err)

	start := time.Now()
	err = su.FetchDaemonStates(func() error {
		t.Fatal("trigger should not run while SendStatesTimeout is holding the semaphore")
		return nil
	})
	assert.ErrorIs(t, err, ErrFetchDaemonStatesSkipped)
	assert.Less(t, time.Since(start), time.Second)

	time.Sleep(250 * time.Millisecond)
}
