/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package fanotify

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/syslog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/nydus-snapshotter/pkg/optimizer/fanotify/conn"
	"github.com/containerd/nydus-snapshotter/pkg/utils/display"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type Server struct {
	BinaryPath                   string
	ContainerPid                 uint32
	ImageName                    string
	PersistFile                  *os.File
	PersistCSVFile               *os.File
	Readable                     bool
	Overwrite                    bool
	Timeout                      time.Duration
	Client                       *conn.Client
	Cmd                          *exec.Cmd
	LogWriter                    *syslog.Writer
	ContainerName                string
	ImageRepo                    string
	IsSent                       bool
	PrefetchPath                 string
	PrefetchDistributionEndpoint string
	Mutex                        sync.Mutex
}

func NewServer(binaryPath string, containerPid uint32, imageName string, file *os.File, csvFile *os.File, readable bool, overwrite bool, timeout time.Duration, logWriter *syslog.Writer, containerName string, imageRepo string, hasSentPrefetchList bool, prefetchPath string, prefetchDistributionEndpoint string) *Server {
	return &Server{
		BinaryPath:                   binaryPath,
		ContainerPid:                 containerPid,
		ImageName:                    imageName,
		PersistFile:                  file,
		PersistCSVFile:               csvFile,
		Readable:                     readable,
		Overwrite:                    overwrite,
		Timeout:                      timeout,
		LogWriter:                    logWriter,
		ContainerName:                containerName,
		ImageRepo:                    imageRepo,
		IsSent:                       hasSentPrefetchList,
		PrefetchPath:                 prefetchPath,
		PrefetchDistributionEndpoint: prefetchDistributionEndpoint,
	}
}

func (fserver *Server) Start() error {
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
		if err := fserver.Receive(); err != nil {
			logrus.WithError(err).Errorf("Failed to receive event information from server")
		}
	}()

	go func() {
		time.Sleep(10 * time.Minute)
		fserver.Mutex.Lock()
		if !fserver.IsSent {
			data, err := getPrefetchListfromLocal(fserver.PrefetchPath)
			if err != nil {
				logrus.WithError(err).Error("error reading file")
			}
			if err = sendToServer(fserver.ImageRepo, fserver.ContainerName, fserver.PrefetchDistributionEndpoint, data); err != nil {
				logrus.WithError(err).Error("failed to send prefetch to http server")
			}
			fserver.IsSent = true
		}
		fserver.Mutex.Unlock()
	}()

	if fserver.Timeout > 0 {
		go func() {
			time.Sleep(fserver.Timeout)
			fserver.Stop()
		}()
	}

	return nil
}

func (fserver *Server) Receive() error {
	defer fserver.PersistFile.Close()
	defer fserver.PersistCSVFile.Close()

	csvWriter := csv.NewWriter(fserver.PersistCSVFile)
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
			fmt.Fprintln(fserver.PersistFile, eventInfo.Path)

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

func (fserver *Server) Stop() {
	fserver.Mutex.Lock()
	if !fserver.IsSent {
		data, err := getPrefetchListfromLocal(fserver.PrefetchPath)
		if err != nil {
			logrus.WithError(err).Errorf("failed to read prefetch files from local")
		}
		if err = sendToServer(fserver.ImageRepo, fserver.ContainerName, fserver.PrefetchDistributionEndpoint, data); err != nil {
			logrus.WithError(err).Errorf("failed to send prefetch list to http server")
		}
		fserver.IsSent = true
	}
	fserver.Mutex.Unlock()

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

type CacheItem struct {
	ImageName     string
	ContainerName string
	PrefetchFiles []string
}

type Cache struct {
	Items map[string]*CacheItem
}

func getPrefetchListfromLocal(prefetchPath string) ([]byte, error) {
	data, err := os.ReadFile(prefetchPath)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func sendToServer(imageName, containerName, serverURL string, data []byte) error {
	filePaths := strings.Split(string(data), "\n")

	var prefetchFiles []string
	for _, path := range filePaths {
		if path != "" {
			prefetchFiles = append(prefetchFiles, path)
		}
	}

	item := CacheItem{
		ImageName:     imageName,
		ContainerName: containerName,
		PrefetchFiles: prefetchFiles,
	}

	err := postRequest(item, serverURL)
	if err != nil {
		return errors.Wrap(err, "error uploading to server")
	}

	return nil
}

func postRequest(item CacheItem, endpoint string) error {
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}

	resp, err := http.Post(endpoint, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.Wrap(fmt.Errorf("post to server returned a non-OK status code: %d", resp.StatusCode), "HTTP Status Error")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrap(err, "failed to read response body")
	}

	logrus.Info("Server Response:", string(body))

	return nil
}
