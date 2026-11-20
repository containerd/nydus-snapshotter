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
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func assertFDClosed(t *testing.T, fd int) {
	t.Helper()
	_, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	assert.ErrorIs(t, err, syscall.EBADF)
}

func TestSupervisorSaveClosesReplacedFD(t *testing.T) {
	su := &Supervisor{fd: -1, dataStorage: newMemStatesStorage()}
	first, err := unix.Dup(int(os.Stdin.Fd()))
	require.NoError(t, err)
	second, err := unix.Dup(int(os.Stdin.Fd()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = unix.Close(second) })

	su.save([]byte("first"), first)
	su.save([]byte("second"), second)

	assertFDClosed(t, first)
	_, err = unix.FcntlInt(uintptr(second), unix.F_GETFD, 0)
	require.NoError(t, err)
}

func TestRecvRejectsMultipleFDsWithoutLeaking(t *testing.T) {
	sockets, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	require.NoError(t, err)
	clientFile := os.NewFile(uintptr(sockets[0]), "client")
	serverFile := os.NewFile(uintptr(sockets[1]), "server")
	t.Cleanup(func() {
		_ = clientFile.Close()
		_ = serverFile.Close()
	})
	client, err := net.FileConn(clientFile)
	require.NoError(t, err)
	server, err := net.FileConn(serverFile)
	require.NoError(t, err)
	clientConn, ok := client.(*net.UnixConn)
	require.True(t, ok)
	serverConn, ok := server.(*net.UnixConn)
	require.True(t, ok)
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	first, err := os.CreateTemp(t.TempDir(), "first")
	require.NoError(t, err)
	defer first.Close()
	second, err := os.CreateTemp(t.TempDir(), "second")
	require.NoError(t, err)
	defer second.Close()

	before, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err)
	_, _, err = clientConn.WriteMsgUnix([]byte("state"), unix.UnixRights(int(first.Fd()), int(second.Fd())), nil)
	require.NoError(t, err)
	require.NoError(t, clientConn.CloseWrite())
	_, fd, err := recv(serverConn)
	after, readErr := os.ReadDir("/proc/self/fd")
	require.NoError(t, readErr)

	assert.EqualError(t, err, "expected exactly one control file descriptor, received 2")
	assert.Equal(t, -1, fd)
	assert.Equal(t, len(before), len(after))
}

func TestDestroySupervisorClosesFD(t *testing.T) {
	set, err := NewSupervisorSet(t.TempDir())
	require.NoError(t, err)
	su := set.NewSupervisor("test")
	fd, err := unix.Dup(int(os.Stdin.Fd()))
	require.NoError(t, err)
	su.save(nil, fd)

	require.NoError(t, set.DestroySupervisor("test"))

	assertFDClosed(t, fd)
	assert.Equal(t, -1, su.fd)
}

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
