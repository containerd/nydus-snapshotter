/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package supervisor

import (
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
		defer conn.Close()

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
