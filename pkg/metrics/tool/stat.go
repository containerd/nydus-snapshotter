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

	"github.com/pkg/errors"
)

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

func GetCurrentStat(pid int) (*Stat, error) {
	uptimeBytes, err := os.ReadFile(path.Join("/proc", "uptime"))
	if err != nil {
		return nil, errors.Wrapf(err, "get uptime failed")
	}
	uptime := ParseFloat64(strings.Split(string(uptimeBytes), " ")[0])

	statBytes, err := os.ReadFile(path.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return nil, errors.Wrapf(err, "get stat failed")
	}
	splitAfterStat := strings.SplitAfter(string(statBytes), ")")

	if len(splitAfterStat) == 0 || len(splitAfterStat) == 1 {
		return nil, errors.Errorf("can not find process, PID: %v", strconv.Itoa(pid))
	}
	infos := strings.Split(splitAfterStat[1], " ")

	files, err := os.ReadDir(path.Join("/proc", strconv.Itoa(pid), "fdinfo"))
	if err != nil {
		return nil, errors.Wrapf(err, "read fdinfo failed")
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
