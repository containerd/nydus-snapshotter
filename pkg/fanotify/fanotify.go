/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package fanotify

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"log/syslog"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/containerd/nydus-snapshotter/pkg/fanotify/conn"
	"github.com/containerd/nydus-snapshotter/pkg/utils/display"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type Server struct {
	BinaryPath   string
	ContainerPid uint32
	ImageName    string
	PersistFile  string
	Readable     bool
	Overwrite    bool
	Timeout      time.Duration
	Client       *conn.Client
	Cmd          *exec.Cmd
	LogWriter    *syslog.Writer
}

func NewServer(binaryPath string, containerPid uint32, imageName string, persistFile string, readable bool, overwrite bool, timeout time.Duration, logWriter *syslog.Writer) *Server {
	return &Server{
		BinaryPath:   binaryPath,
		ContainerPid: containerPid,
		ImageName:    imageName,
		PersistFile:  persistFile,
		Readable:     readable,
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

	go func() {
		if err := cmd.Wait(); err != nil {
			logrus.WithError(err).Errorf("Failed to wait for fserver to finish")
		}
	}()

	go func() {
		if err := fserver.RunReceiver(); err != nil {
			logrus.WithError(err).Errorf("Failed to receive event information from server")
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

func (fserver *Server) RunReceiver() error {
	f, err := os.OpenFile(fserver.PersistFile, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return errors.Wrapf(err, "failed to open file %q", fserver.PersistFile)
	}
	defer f.Close()

	persistCsvFile := fmt.Sprintf("%s.csv", fserver.PersistFile)
	fCsv, err := os.Create(persistCsvFile)
	if err != nil {
		return errors.Wrapf(err, "failed to create file %q", persistCsvFile)
	}
	defer fCsv.Close()

	csvWriter := csv.NewWriter(fCsv)
	if err := csvWriter.Write([]string{"path", "size", "elapsed"}); err != nil {
		return errors.Wrapf(err, "failed to write csv header")
	}
	csvWriter.Flush()

	for {
		eventInfo, err := fserver.Client.GetEventInfo()
		if err != nil {
			if err == io.EOF {
				logrus.Infoln("Get EOF from fanotify server, break event receiver")
				break
			}
			return fmt.Errorf("failed to get event information: %v", err)
		}

		if eventInfo != nil {
			fmt.Fprintln(f, eventInfo.Path)

			var line []string
			if fserver.Readable {
				line = []string{eventInfo.Path, display.ByteToReadableIEC(eventInfo.Size), display.MicroSecondToReadable(eventInfo.Elapsed)}
			} else {
				line = []string{eventInfo.Path, fmt.Sprint(eventInfo.Size), fmt.Sprint(eventInfo.Elapsed)}
			}
			if err := csvWriter.Write(line); err != nil {
				return errors.Wrapf(err, "failed to write csv")
			}
			csvWriter.Flush()
		}
	}

	return nil
}

func (fserver *Server) StopServer() {
	if fserver.Cmd != nil {
		logrus.Infof("Send SIGTERM signal to process group %d", fserver.Cmd.Process.Pid)
		if err := syscall.Kill(-fserver.Cmd.Process.Pid, syscall.SIGTERM); err != nil {
			logrus.WithError(err).Errorf("Stop process group %d failed!", fserver.Cmd.Process.Pid)
		}
		if _, err := fserver.Cmd.Process.Wait(); err != nil {
			logrus.WithError(err).Errorf("Failed to wait for fanotify server")
		}
	}
}
