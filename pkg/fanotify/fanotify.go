/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package fanotify

import (
	"bufio"
	"fmt"
	"io"
	"log/syslog"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/containerd/nydus-snapshotter/pkg/fanotify/conn"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func StartFanotifier(client *conn.Client, persistFile string) error {
	f, err := os.OpenFile(persistFile, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return errors.Wrapf(err, "failed to open persist file %q", persistFile)
	}
	var existedFiles = make(map[string]struct{}, 1)
	for {
		path, err := client.GetPath()
		if err != nil {
			f.Close()
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to get notified path: %v", err)
		}

		if _, ok := existedFiles[path]; !ok {
			existedFiles[path] = struct{}{}
			fmt.Fprintln(f, path)
		}
	}
	return nil
}

type Server struct {
	BinaryPath   string
	ContainerPid uint32
	ImageName    string
	PersistFile  string
	Overwrite    bool
	Timeout      time.Duration
	Client       *conn.Client
	Cmd          *exec.Cmd
	LogWriter    *syslog.Writer
}

func NewServer(binaryPath string, containerPid uint32, imageName string, persistFile string, overwrite bool, timeout time.Duration, logWriter *syslog.Writer) *Server {
	return &Server{
		BinaryPath:   binaryPath,
		ContainerPid: containerPid,
		ImageName:    imageName,
		PersistFile:  persistFile,
		Overwrite:    overwrite,
		Timeout:      timeout,
		LogWriter:    logWriter,
	}
}

func (fserver *Server) RunServer() error {
	if !fserver.Overwrite {
		if file, err := os.Stat(fserver.PersistFile); err == nil && !file.IsDir() {
			return nil
		}
	}

	cmd := exec.Command(fserver.BinaryPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNS,
		Setpgid:    true,
	}
	cmd.Env = append(cmd.Env, "_MNTNS_PID="+fmt.Sprint(fserver.ContainerPid))
	cmd.Env = append(cmd.Env, "_TARGET=/")
	cmd.Stderr = fserver.LogWriter

	notifyR, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	fserver.Client = &conn.Client{
		Scanner: bufio.NewScanner(notifyR),
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	fserver.Cmd = cmd

	go func() {
		if err := StartFanotifier(fserver.Client, fserver.PersistFile); err != nil {
			logrus.WithError(err).Errorf("Start files scanner failed!")
		}
	}()

	if fserver.Timeout > 0 {
		go func() {
			time.Sleep(fserver.Timeout)
			fserver.StopServer()
		}()
	}

	return nil
}

func (fserver *Server) StopServer() {
	if fserver.Cmd != nil {
		if err := fserver.Cmd.Process.Kill(); err == nil {
			if _, err := fserver.Cmd.Process.Wait(); err != nil {
				logrus.WithError(err).Errorf("Failed to wait for fanotify server")
			}
		}
	}
}
