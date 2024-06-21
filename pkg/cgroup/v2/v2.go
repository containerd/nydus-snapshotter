/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package v2

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/cgroups/v3/cgroup2"
	"github.com/containerd/log"
	"golang.org/x/exp/slices"
)

const (
	defaultRoot = "/sys/fs/cgroup"
)

var (
	ErrRootMemorySubtreeControllerDisabled = errors.New("cgroups v2: root subtree controller for memory is disabled")
)

type Cgroup struct {
	manager *cgroup2.Manager
}

func readSubtreeControllers(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "cgroup.subtree_control"))
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(b)), nil
}

func NewCgroup(slice, name string, memoryLimitInBytes int64) (Cgroup, error) {
	resources := &cgroup2.Resources{
		Memory: &cgroup2.Memory{},
	}
	if memoryLimitInBytes > -1 {
		resources = &cgroup2.Resources{
			Memory: &cgroup2.Memory{
				Max: &memoryLimitInBytes,
			},
		}
	}

	rootSubtreeControllers, err := readSubtreeControllers(defaultRoot)
	if err != nil {
		return Cgroup{}, err
	}
	log.L.Infof("root subtree controllers: %s", rootSubtreeControllers)

	if !slices.Contains(rootSubtreeControllers, "memory") {
		return Cgroup{}, ErrRootMemorySubtreeControllerDisabled
	}

	m, err := cgroup2.NewManager(defaultRoot, fmt.Sprintf("/%s/%s", slice, name), resources)
	if err != nil {
		return Cgroup{}, err
	}

	controllers, err := m.Controllers()
	if err != nil {
		return Cgroup{}, err
	}
	log.L.Infof("create cgroup (v2) successful, controllers: %v", controllers)

	return Cgroup{
		manager: m,
	}, nil
}

func (cg Cgroup) Delete() error {
	if cg.manager != nil {
		return cg.manager.Delete()
	}
	return nil
}
func (cg Cgroup) AddProc(pid int) error {
	if cg.manager != nil {
		err := cg.manager.AddProc(uint64(pid))
		if err != nil {
			return err
		}
		log.L.Infof("add process %d to daemon cgroup successful", pid)
	}
	return nil
}
