/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemon

import "sync"

type DaemonProcessStatus string

const (
	DaemonProcessStatusUnknown DaemonProcessStatus = "UNKNOWN"
	DaemonProcessStatusRunning DaemonProcessStatus = "RUNNING"
	DaemonProcessStatusExited  DaemonProcessStatus = "EXITED"
)

type DaemonProcess struct {
	mu     sync.Mutex
	Status DaemonProcessStatus
}

func (p *DaemonProcess) SetStatus(s DaemonProcessStatus) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.Status = s
}

func (p *DaemonProcess) IsExitedStatus() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.Status == DaemonProcessStatusExited
}
