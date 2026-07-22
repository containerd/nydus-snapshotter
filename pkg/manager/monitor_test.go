/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	"context"
	"log"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/sys/unix"
)

func startUnixServer(ctx context.Context, sock string) {

	os.RemoveAll(sock)

	listener, err := net.Listen("unix", sock)
	if err != nil {
		log.Fatal(err)
	}

	var conn net.Conn
	conn, err = listener.Accept()
	if err != nil {
		log.Fatal()
	}

	for {
		select {
		case <-ctx.Done():
			conn.Close()
			return
		default:
			time.Sleep(200 * time.Millisecond)
		}
	}

}

func TestLivenessMonitorSubscribeReportsArmFailure(t *testing.T) {
	sock, err := os.CreateTemp("", "liveness_monitor_sock")
	assert.Nil(t, err)
	sock.Close()
	t.Cleanup(func() { os.Remove(sock.Name()) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go startUnixServer(ctx, sock.Name())

	monitor, err := newMonitor()
	assert.Nil(t, err)
	assert.NotNil(t, monitor)
	time.Sleep(time.Millisecond * 200)

	// Sabotage the epoll fd so that arming the subscription (EPOLL_CTL_ADD)
	// fails. Subscribe must report the failure instead of pretending the
	// daemon is monitored: an unmonitored daemon would never produce a death
	// event, so its recovery would never start.
	assert.Nil(t, unix.Close(monitor.epollFd))

	notifier := make(chan deathEvent, 1)
	err = monitor.Subscribe("daemon_1", sock.Name(), notifier)
	assert.NotNil(t, err)

	// All-or-nothing: no half-registered state may remain.
	assert.Equal(t, 0, len(monitor.subscribers))
	assert.Equal(t, 0, len(monitor.set))
}

func TestLivenessMonitor(t *testing.T) {
	sockPattern := "liveness_monitor_sock"

	s1, err1 := os.CreateTemp("", sockPattern)
	assert.Nil(t, err1)
	s1.Close()

	s2, err2 := os.CreateTemp("", sockPattern)
	assert.Nil(t, err2)
	s2.Close()

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())

	go startUnixServer(ctx1, s1.Name())
	go startUnixServer(ctx2, s2.Name())

	monitor, _ := newMonitor()
	assert.NotNil(t, monitor)

	time.Sleep(time.Millisecond * 200)

	notifier := make(chan deathEvent, 10)

	e1 := monitor.Subscribe("daemon_1", s1.Name(), notifier)
	assert.Nil(t, e1)
	e1 = monitor.Subscribe("daemon_1", s1.Name(), notifier)
	assert.NotNil(t, e1)
	e2 := monitor.Subscribe("daemon_2", s2.Name(), notifier)
	assert.Nil(t, e2)

	t.Cleanup(func() {
		os.Remove(s1.Name())
		os.Remove(s2.Name())
	})

	monitor.Run()

	time.Sleep(time.Millisecond * 200)

	// Daemon 1 dies and unblock from channel `n1`
	cancel1()
	event := <-notifier
	assert.Equal(t, event.daemonID, "daemon_1")

	err := monitor.Unsubscribe("daemon_2")
	assert.Nil(t, err)
	cancel2()

	// Should not block here.
	assert.Equal(t, len(notifier), 0)

	time.Sleep(time.Second * 1)

	monitor.Destroy()

	assert.Equal(t, len(monitor.set), 0)
	assert.Equal(t, len(monitor.subscribers), 0)
}
