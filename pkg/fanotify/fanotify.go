/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package fanotify

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/syslog"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/containerd/nydus-snapshotter/pkg/fanotify/conn"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

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
		Reader: bufio.NewReader(notifyR),
	}

	if err := cmd.Start(); err != nil {
		return err
	}
	fserver.Cmd = cmd

	if fserver.Timeout > 0 {
		go func() {
			time.Sleep(fserver.Timeout)
			fserver.StopServer()
		}()
	}

	return nil
}

func (fserver *Server) ReceiveEventInfo() error {
	eventInfo, err := fserver.Client.GetEventInfo()
	if err != nil {
		return fmt.Errorf("failed to get event information: %v", err)
	}

	f, err := os.OpenFile(fserver.PersistFile, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return errors.Wrapf(err, "failed to open persist file %q", fserver.PersistFile)
	}
	defer f.Close()

	for _, event := range eventInfo {
		fmt.Fprintln(f, event.Path)
	}

	persistJSONFile := fmt.Sprintf("%s.json", fserver.PersistFile)
	data, err := json.MarshalIndent(eventInfo, "", "      ")
	if err != nil {
		return errors.Wrapf(err, "failed to encode event information %v", eventInfo)
	}
	if err := os.WriteFile(persistJSONFile, data, 0644); err != nil {
		return errors.Wrapf(err, "failed to write file %s", persistJSONFile)
	}

	return nil
}

func (fserver *Server) StopServer() {
	if fserver.Cmd != nil {
		logrus.Infof("Send SIGTERM signal to process group %d", fserver.Cmd.Process.Pid)
		if err := syscall.Kill(-fserver.Cmd.Process.Pid, syscall.SIGTERM); err != nil {
			logrus.WithError(err).Errorf("Stop process group %d failed!", fserver.Cmd.Process.Pid)
		}
		if err := fserver.ReceiveEventInfo(); err != nil {
			logrus.WithError(err).Errorf("Failed to receive event information")
		}
		if _, err := fserver.Cmd.Process.Wait(); err != nil {
			logrus.WithError(err).Errorf("Failed to wait for fanotify server")
		}
	}
}
