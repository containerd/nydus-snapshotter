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
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/containerd/console"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/stargz-snapshotter/analyzer/fanotify"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/spf13/cobra"
)

type Context struct {
	fanotifier         *fanotify.Fanotifier
	accessedFiles      []string
	persistentPath     string
	WaitSignal         bool
	WaitLine           string
	WaitTime           time.Duration
	terminal           bool
	stdin              bool
	fanotifierClosed   bool
	fanotifierClosedMu sync.Mutex
}

func GenerateFanotifyOpts(ctx context.Context, cmd *cobra.Command, flagI, flagT bool) ([]oci.SpecOpts, *Context, error) {
	var opts []oci.SpecOpts
	// Spawn a fanotifier process in a new mount namespace.
	fanotifier, err := fanotify.SpawnFanotifier("/proc/self/exe")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to spawn fanotifier: %w", err)
	}
	opts = append(opts, oci.WithLinuxNamespace(runtimespec.LinuxNamespace{
		Type: runtimespec.MountNamespace,
		Path: fanotifier.MountNamespacePath(), // use mount namespace that the fanotifier created
	}))

	waitTime, err := cmd.Flags().GetInt64("wait-time")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get wait time: %w", err)
	}
	waitLine, err := cmd.Flags().GetString("wait-line")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get wait line: %w", err)
	}
	waitSignal, err := cmd.Flags().GetBool("wait-signal")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get wait signal: %w", err)
	}
	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get output: %w", err)
	}

	if flagT && waitSignal {
		return nil, nil, fmt.Errorf("wait-signal cannot be used with terminal option")
	}

	return opts, &Context{
		fanotifier:     fanotifier,
		accessedFiles:  make([]string, 0),
		persistentPath: output,
		WaitTime:       time.Duration(waitTime) * time.Second,
		WaitLine:       waitLine,
		WaitSignal:     waitSignal,
		stdin:          flagI,
		terminal:       flagT,
	}, nil
}

func (fanotifierCtx *Context) StartFanotifyMonitor() error {
	if fanotifierCtx == nil {
		return fmt.Errorf("fanotifierCtx is nil")
	}
	if fanotifierCtx.fanotifier == nil {
		return fmt.Errorf("fanotifier is nil")
	}

	if err := fanotifierCtx.fanotifier.Start(); err != nil {
		return fmt.Errorf("failed to start fanotifier: %w", err)
	}

	persistentFd, err := os.Create(fanotifierCtx.persistentPath)
	if err != nil {
		persistentFd.Close()
		return err
	}

	go func() {
		for {
			path, err := fanotifierCtx.fanotifier.GetPath()
			if err != nil {
				if err == io.EOF {
					fanotifierCtx.fanotifierClosedMu.Lock()
					var isFanotifierClosed = fanotifierCtx.fanotifierClosed
					fanotifierCtx.fanotifierClosedMu.Unlock()
					if isFanotifierClosed {
						break
					}
				}
				break
			}
			if !fanotifierCtx.accessedFileExist(path) {
				fmt.Fprintln(persistentFd, path)
				fanotifierCtx.accessedFiles = append(fanotifierCtx.accessedFiles, path)
			}
		}
	}()

	return nil
}

func (fanotifierCtx *Context) PrepareWaiter(ctx context.Context, waiterCtx *WaiterContext) error {
	if fanotifierCtx == nil {
		return fmt.Errorf("fanotifierCtx is nil")
	}

	var ioCreator cio.Creator
	var con console.Console
	lineWaiter := &lineWaiter{
		waitCh:         make(chan string),
		waitLineString: fanotifierCtx.WaitLine,
	}
	stdinC := &lazyReadCloser{reader: os.Stdin, initCond: sync.NewCond(&sync.Mutex{})}
	if fanotifierCtx.terminal {
		if !fanotifierCtx.stdin {
			return fmt.Errorf("terminal cannot be used if stdin isn't enabled")
		}
		con = console.Current()
		defer con.Reset()
		if err := con.SetRaw(); err != nil {
			return err
		}
		// On terminal mode, the "stderr" field is unused.
		ioCreator = cio.NewCreator(cio.WithStreams(con, lineWaiter.registerWriter(con), nil), cio.WithTerminal)
	} else {
		if fanotifierCtx.stdin {
			ioCreator = cio.NewCreator(cio.WithStreams(stdinC, lineWaiter.registerWriter(os.Stdout), os.Stderr))
		} else {
			ioCreator = cio.NewCreator(cio.WithStreams(nil, lineWaiter.registerWriter(os.Stdout), os.Stderr))
		}
	}

	waiterCtx.IoCreator = &ioCreator
	waiterCtx.LineWaiter = lineWaiter
	waiterCtx.ReadCloser = stdinC
	waiterCtx.Console = &con

	return nil
}

func (fanotifierCtx *Context) StartWaiter(ctx context.Context, container containerd.Container, task containerd.Task, waiterCtx *WaiterContext, flagD, rm bool) error {
	if fanotifierCtx == nil {
		return fmt.Errorf("fanotifierCtx is nil")
	}
	if waiterCtx == nil {
		return fmt.Errorf("waiterCtx is nil")
	}
	var err error
	var code uint32
	if fanotifierCtx.WaitSignal || fanotifierCtx.WaitLine != "" || fanotifierCtx.WaitTime > 0 {
		var status containerd.ExitStatus
		var killOk bool
		// Wait until the task exit
		if fanotifierCtx.WaitSignal { // NOTE: not functional with `terminal` option
			logrus.Infoln("press Ctrl+C to terminate the container")
			status, killOk, err = waitSignalHandler(ctx, container, task)
			if err != nil {
				return err
			}
		} else {
			if fanotifierCtx.WaitLine != "" {
				logrus.Infof("waiting for line \"%v\" ...", fanotifierCtx.WaitLine)
				status, killOk, err = waitLineHandler(ctx, container, task, waiterCtx.LineWaiter)
				if err != nil {
					return err
				}
			} else {
				logrus.Infof("waiting for %v ...", fanotifierCtx.WaitTime)
				status, killOk, err = waitTimeHandler(ctx, container, task, fanotifierCtx.WaitTime)
				if err != nil {
					return err
				}
			}
		}
		if !killOk {
			logrus.Warnf("failed to exit task %v; manually kill it", task.ID())
		} else {
			code, _, err = status.Result()
			if err != nil {
				return err
			}
			if _, err := task.Delete(ctx); err != nil {
				return err
			}
		}

		fanotifierCtx.fanotifierClosedMu.Lock()
		fanotifierCtx.fanotifierClosed = true
		fanotifierCtx.fanotifierClosedMu.Unlock()

		if fanotifierCtx.fanotifier != nil {
			if err := fanotifierCtx.fanotifier.Close(); err != nil {
				return fmt.Errorf("failed to cleanup fanotifier")
			}
		}
	} else {
		var statusC <-chan containerd.ExitStatus
		if !flagD {
			defer func() {
				if rm {
					if _, taskDeleteErr := task.Delete(ctx); taskDeleteErr != nil {
						logrus.Error(taskDeleteErr)
					}
				}
			}()
			statusC, err = task.Wait(ctx)
			if err != nil {
				return err
			}
		}

		status := <-statusC
		code, _, err = status.Result()
		if err != nil {
			return err
		}
	}
	waiterCtx.ExitCode = code
	logrus.Infof("container exit with code %v", code)

	return nil
}

func (fanotifierCtx *Context) accessedFileExist(filePath string) bool {
	tmpAccessedFiles := make([]string, len(fanotifierCtx.accessedFiles))
	copy(tmpAccessedFiles, fanotifierCtx.accessedFiles)
	sort.Strings(tmpAccessedFiles)
	if index := sort.SearchStrings(tmpAccessedFiles, filePath); index < len(tmpAccessedFiles) && tmpAccessedFiles[index] == filePath {
		return true
	}
	return false
}
