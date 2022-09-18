/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package supervisor

import (
	"net"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/sys/unix"
)

func TestSupervisor(t *testing.T) {
	rootDir, err1 := os.MkdirTemp("", "supervisor")
	assert.Nil(t, err1)

	t.Cleanup(func() {
		os.RemoveAll(rootDir)
	})

	supervisorSet, err := NewSupervisorSet(rootDir)
	assert.Nil(t, err, "%v", err)

	su1 := supervisorSet.NewSupervisor("su1")
	assert.NotNil(t, su1)

	err = su1.WaitForStatesTimeout(2 * time.Second)
	assert.Nil(t, err, "%v", err)
	sock := su1.Sock()

	addr, err := net.ResolveUnixAddr("unix", sock)
	assert.Nil(t, err)

	conn, err := net.DialUnix("unix", nil, addr)
	assert.Nil(t, err, "%v", err)

	sentData := []byte("abcde")

	sentLen, err := conn.Write(sentData)
	assert.Nil(t, err)

	conn.Close()

	// FIXME: Delay for some time until states are stored
	time.Sleep(500 * time.Millisecond)

	// Must set length not only capacity
	receivedData := make([]byte, 16, 32)
	oob := make([]byte, 16, 32)
	err = su1.SendStatesTimeout(0)
	assert.Nil(t, err, "%v", err)

	conn1, err := net.DialUnix("unix", nil, addr)
	assert.Nil(t, err, "%v", err)

	f, _ := conn1.File()

	//nolint:dogsled
	receivedLen, _, _, _, err := unix.Recvmsg(int(f.Fd()), receivedData, oob, 0)
	assert.Nil(t, err)

	assert.Equal(t, sentLen, receivedLen)
	assert.True(t, reflect.DeepEqual(receivedData[:receivedLen], sentData), "%v", receivedData)

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
