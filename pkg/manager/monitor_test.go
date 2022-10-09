//go:build linux
// +build linux

/*
   Copyright The nydus Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
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

	monitor.Unsubscribe("daemon_2")
	cancel2()

	// Should not block here.
	assert.Equal(t, len(notifier), 0)

	time.Sleep(time.Second * 1)

	monitor.Destroy()

	assert.Equal(t, len(monitor.set), 0)
	assert.Equal(t, len(monitor.subscribers), 0)
}
