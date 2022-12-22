/*
   Copyright The containerd Authors.

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

package fanotify

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/containerd/console"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/docker/docker/errdefs"
	"github.com/sirupsen/logrus"
)

type WaiterContext struct {
	IoCreator  *cio.Creator
	LineWaiter *lineWaiter
	ReadCloser *lazyReadCloser
	Console    *console.Console
	ExitCode   uint32
}

type lineWaiter struct {
	waitCh         chan string
	waitLineString string
}

type lazyReadCloser struct {
	reader      io.Reader
	closer      func()
	closerMutex sync.Mutex
	initCond    *sync.Cond
	initialized int64
}

func (s *lazyReadCloser) RegisterCloser(closer func()) {
	s.closerMutex.Lock()
	s.closer = closer
	s.closerMutex.Unlock()
	atomic.AddInt64(&s.initialized, 1)
	s.initCond.Broadcast()
}
func (s *lazyReadCloser) Read(p []byte) (int, error) {
	if atomic.LoadInt64(&s.initialized) <= 0 {
		// wait until initialized
		s.initCond.L.Lock()
		if atomic.LoadInt64(&s.initialized) <= 0 {
			s.initCond.Wait()
		}
		s.initCond.L.Unlock()
	}

	n, err := s.reader.Read(p)
	if err == io.EOF {
		s.closerMutex.Lock()
		s.closer()
		s.closerMutex.Unlock()
	}
	return n, err
}

func (lw *lineWaiter) registerWriter(w io.Writer) io.Writer {
	if lw.waitLineString == "" {
		return w
	}

	pr, pw := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), lw.waitLineString) {
				lw.waitCh <- lw.waitLineString
			}
		}
		if _, err := io.Copy(io.Discard, pr); err != nil {
			pr.CloseWithError(err)
			return
		}
	}()

	return io.MultiWriter(w, pw)
}

func waitSignalHandler(ctx context.Context, container containerd.Container, task containerd.Task) (containerd.ExitStatus, bool, error) {
	statusC, err := task.Wait(ctx)
	if err != nil {
		return containerd.ExitStatus{}, false, err
	}
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT)
	defer signal.Stop(sc)
	select {
	case status := <-statusC:
		return status, true, nil
	case <-sc:
		logrus.Infoln("signal detected")
		status, err := killTask(ctx, container, task, statusC)
		if err != nil {
			logrus.Errorln("failed to kill container")
			return containerd.ExitStatus{}, false, err
		}
		return status, true, nil
	}
}

func waitTimeHandler(ctx context.Context, container containerd.Container, task containerd.Task, waitTime time.Duration) (containerd.ExitStatus, bool, error) {
	statusC, err := task.Wait(ctx)
	if err != nil {
		return containerd.ExitStatus{}, false, err
	}
	select {
	case status := <-statusC:
		return status, true, nil
	case <-time.After(waitTime):
		logrus.Warnf("killing task. the wait time (%s) reached", waitTime.String())
	}
	status, err := killTask(ctx, container, task, statusC)
	if err != nil {
		logrus.Warnln("failed to kill container")
		return containerd.ExitStatus{}, false, err
	}
	return status, true, nil
}

func waitLineHandler(ctx context.Context, container containerd.Container, task containerd.Task, waitLine *lineWaiter) (containerd.ExitStatus, bool, error) {
	if waitLine == nil {
		return containerd.ExitStatus{}, false, fmt.Errorf("lineWaiter is nil")
	}

	statusC, err := task.Wait(ctx)
	if err != nil {
		return containerd.ExitStatus{}, false, err
	}
	select {
	case status := <-statusC:
		return status, true, nil
	case l := <-waitLine.waitCh:
		logrus.Infof("Waiting line detected %q, killing task", l)
	}
	status, err := killTask(ctx, container, task, statusC)
	if err != nil {
		logrus.Warnln("failed to kill container")
		return containerd.ExitStatus{}, false, err
	}
	return status, true, nil
}

func killTask(ctx context.Context, container containerd.Container, task containerd.Task, statusC <-chan containerd.ExitStatus) (containerd.ExitStatus, error) {
	sig, err := containerd.GetStopSignal(ctx, container, syscall.SIGKILL)
	if err != nil {
		return containerd.ExitStatus{}, err
	}
	if err := task.Kill(ctx, sig, containerd.WithKillAll); err != nil && !errdefs.IsNotFound(err) {
		return containerd.ExitStatus{}, fmt.Errorf("forward SIGKILL: %w", err)
	}
	select {
	case status := <-statusC:
		return status, nil
	case <-time.After(5 * time.Second):
		return containerd.ExitStatus{}, fmt.Errorf("forward SIGKILL: %w", err)
	}
}
