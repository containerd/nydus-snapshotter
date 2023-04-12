/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tool

import (
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/pkg/errors"
)

// Please refer to https://man7.org/linux/man-pages/man5/proc.5.html for the metrics meanings
type Stat struct {
	Utime  float64
	Stime  float64
	Cutime float64
	Cstime float64
	Thread float64
	Start  float64
	Rss    float64
	Fds    float64
	Uptime float64
}

var (
	ClkTck   = GetClkTck()
	PageSize = GetPageSize()
)

func CalculateCPUUtilization(begin *Stat, now *Stat) (float64, error) {
	if begin == nil || now == nil {
		return 0.0, errdefs.ErrInvalidArgument
	}

	cpuSys := (now.Stime - begin.Stime) / ClkTck
	cpuUsr := (now.Utime - begin.Utime) / ClkTck
	total := cpuSys + cpuUsr

	seconds := now.Uptime - begin.Uptime

	cpuPercent := (total / seconds) * 100

	return cpuPercent, nil
}

func GetProcessMemoryRSSKiloBytes(pid int) (float64, error) {
	stat, err := GetProcessStat(pid)
	if err != nil {
		return 0.0, errors.Wrapf(err, "get process stat")
	}

	return stat.Rss * PageSize / 1024, nil
}

func GetProcessStat(pid int) (*Stat, error) {
	uptimeBytes, err := os.ReadFile(path.Join("/proc", "uptime"))
	if err != nil {
		return nil, errors.Wrapf(err, "get uptime")
	}
	uptime := ParseFloat64(strings.Split(string(uptimeBytes), " ")[0])

	statBytes, err := os.ReadFile(path.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return nil, errors.Wrapf(err, "get process %d stat", pid)
	}
	splitAfterStat := strings.SplitAfter(string(statBytes), ")")

	if len(splitAfterStat) == 0 || len(splitAfterStat) == 1 {
		return nil, errors.Errorf("Can not find process, PID: %d", pid)
	}
	infos := strings.Split(splitAfterStat[1], " ")

	files, err := os.ReadDir(path.Join("/proc", strconv.Itoa(pid), "fdinfo"))
	if err != nil {
		return nil, errors.Wrapf(err, "read fdinfo")
	}

	return &Stat{
		Utime:  ParseFloat64(infos[12]),
		Stime:  ParseFloat64(infos[13]),
		Cutime: ParseFloat64(infos[14]),
		Cstime: ParseFloat64(infos[15]),
		Thread: ParseFloat64(infos[18]),
		Start:  ParseFloat64(infos[20]),
		Rss:    ParseFloat64(infos[22]),
		Fds:    float64(len(files)),
		Uptime: uptime,
	}, nil
}

func GetProcessRunningState(pid int) (string, error) {
	statBytes, err := os.ReadFile(path.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", errors.Wrapf(err, "get process %d stat", pid)
	}

	segments := strings.Split(string(statBytes), " ")
	state := segments[2]
	return state, nil
}

func IsZombieProcess(pid int) (bool, error) {
	s, err := GetProcessRunningState(pid)
	if err != nil {
		return false, err
	}

	return s == "Z", nil
}
