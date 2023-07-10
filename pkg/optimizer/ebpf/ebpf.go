/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package ebpf

import (
	"encoding/csv"
	"fmt"
	"log/syslog"
	"os"
	"time"

	"github.com/containerd/nydus-snapshotter/pkg/optimizer/ebpf/conn"
	"github.com/containerd/nydus-snapshotter/pkg/utils/display"
	bpf "github.com/iovisor/gobpf/bcc"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type Server struct {
	ContainerID    string
	BPFTable       *bpf.Table
	Module         *bpf.Module
	PerfMap        *bpf.PerfMap
	Client         *conn.Client
	Close          chan struct{}
	ImageName      string
	PersistFile    *os.File
	PersistCSVFile *os.File
	Readable       bool
	Overwrite      bool
	Timeout        time.Duration
	LogWriter      *syslog.Writer
}

func NewServer(containerID string, imageName string, file *os.File, csvFile *os.File, readable bool, overwrite bool, timeout time.Duration, logWriter *syslog.Writer) Server {
	return Server{
		ContainerID:    containerID,
		ImageName:      imageName,
		PersistFile:    file,
		PersistCSVFile: csvFile,
		Readable:       readable,
		Overwrite:      overwrite,
		Timeout:        timeout,
		LogWriter:      logWriter,
	}
}

func (eserver Server) Start() error {
	go func() {
		m, table, err := conn.InitKprobeTable(eserver.ContainerID)
		if err != nil {
			logrus.Infof("InitKprobeTable err: %v", err)
			return
		}

		channel := make(chan []byte)
		eserver.PerfMap, err = bpf.InitPerfMapWithPageCnt(table, channel, nil, 1024)
		if err != nil {
			logrus.Infof("init perf map err: %v", err)
			return
		}
		eserver.Module = m
		eserver.Client = &conn.Client{
			Channel: channel,
		}
		eserver.Close = make(chan struct{})

		go func() {
			if err := eserver.Receive(); err != nil {
				logrus.WithError(err).Errorf("Failed to receive event information from server")
			}
		}()

		eserver.PerfMap.Start()

		if eserver.Timeout > 0 {
			go func() {
				time.Sleep(eserver.Timeout)
				eserver.Stop()
			}()
		}

	}()

	return nil
}

func (eserver Server) Stop() {
	if eserver.PerfMap != nil {
		eserver.PerfMap.Stop()
		eserver.Close <- struct{}{}
		eserver.Module.Close()
	}
}

func (eserver Server) Receive() error {
	defer eserver.PersistFile.Close()
	defer eserver.PersistCSVFile.Close()

	csvWriter := csv.NewWriter(eserver.PersistCSVFile)
	if err := csvWriter.Write([]string{"timestamp", "command", "path", "position", "size"}); err != nil {
		return errors.Wrapf(err, "failed to write csv header")
	}
	csvWriter.Flush()

	fileList := make(map[string]struct{})
	for {
		select {
		case <-eserver.Close:
			close(eserver.Close)
			for key := range fileList {
				delete(fileList, key)
			}
			return nil
		default:
			eventInfo, err := eserver.Client.GetEventInfo()
			if err != nil {
				return fmt.Errorf("failed to get event information: %v", err)
			}

			if eventInfo != nil {
				if _, ok := fileList[eventInfo.Path]; !ok {
					fmt.Fprintln(eserver.PersistFile, eventInfo.Path)
					fileList[eventInfo.Path] = struct{}{}
				}

				var line []string
				if eserver.Readable {
					eventTime := time.Unix(0, eventInfo.Timestamp*int64(time.Millisecond)).Format("2006-01-02 15:04:05.000")
					line = []string{eventTime, eventInfo.Command, eventInfo.Path, fmt.Sprint(eventInfo.Position), display.ByteToReadableIEC(eventInfo.Size)}
				} else {
					line = []string{fmt.Sprint(eventInfo.Timestamp), eventInfo.Command, eventInfo.Path, fmt.Sprint(eventInfo.Position), fmt.Sprint(eventInfo.Size)}
				}
				if err := csvWriter.Write(line); err != nil {
					return errors.Wrapf(err, "failed to write csv")
				}
				csvWriter.Flush()
			}
		}
	}
}
