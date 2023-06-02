/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package sysinfo

import (
	"sync"
	"syscall"
)

var (
	sysinfo     *syscall.Sysinfo_t
	sysinfoOnce sync.Once
	sysinfoErr  error
)

func GetSysinfo() {
	var info syscall.Sysinfo_t
	err := syscall.Sysinfo(&info)
	if err != nil {
		sysinfoErr = err
		return
	}
	sysinfo = &info
	sysinfoErr = nil
}

func GetTotalMemoryBytes() (int, error) {
	sysinfoOnce.Do(GetSysinfo)
	if sysinfo == nil {
		return 0, sysinfoErr
	}

	return int(sysinfo.Totalram), nil
}
