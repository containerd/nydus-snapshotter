/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package cgroup

import (
	"github.com/containerd/log"
)

type Manager struct {
	name   string
	config Config
	cgroup DaemonCgroup
}

type Opt struct {
	Name   string
	Config Config
}

func NewManager(opt Opt) (*Manager, error) {
	if !supported() {
		log.L.Warn("cgroup is not supported")
		return nil, ErrCgroupNotSupported
	}

	log.L.Infof("cgroup mode: %s", displayMode())
	cg, err := createCgroup(opt.Name, opt.Config)
	if err != nil {
		return nil, err
	}

	return &Manager{
		name:   opt.Name,
		config: opt.Config,
		cgroup: cg,
	}, nil
}

// Please make sure the *Manager is not null.
func (m *Manager) AddProc(pid int) error {
	return m.cgroup.AddProc(pid)
}

// Please make sure the *Manager is not null.
func (m *Manager) Delete() error {
	return m.cgroup.Delete()
}
