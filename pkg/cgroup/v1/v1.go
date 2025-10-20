/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package v1

import (
	"github.com/containerd/cgroups/v3/cgroup1"
	"github.com/containerd/log"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

type Cgroup struct {
	controller cgroup1.Cgroup
}

func generateHierarchy() cgroup1.Hierarchy {
	return cgroup1.SingleSubsystem(cgroup1.Default, cgroup1.Memory)
}

func NewCgroup(slice, name string, memoryLimitInBytes int64) (Cgroup, error) {
	hierarchy := generateHierarchy()
	specResources := &specs.LinuxResources{
		Memory: &specs.LinuxMemory{
			Limit: &memoryLimitInBytes,
		},
	}

	controller, err := cgroup1.Load(cgroup1.Slice(slice, name), cgroup1.WithHiearchy(hierarchy))
	if err != nil && err != cgroup1.ErrCgroupDeleted {
		return Cgroup{}, err
	}

	if controller != nil {
		processes, err := controller.Processes(cgroup1.Memory, true)
		if err != nil {
			return Cgroup{}, err
		}
		if len(processes) > 0 {
			log.L.Infof("target cgroup is existed with processes %v", processes)
			if err := controller.Update(specResources); err != nil {
				return Cgroup{}, err
			}
			return Cgroup{
				controller: controller,
			}, nil
		}
		if err := controller.Delete(); err != nil {
			return Cgroup{}, err
		}
	}

	controller, err = cgroup1.New(cgroup1.Slice(slice, name), specResources, cgroup1.WithHiearchy(hierarchy))
	if err != nil {
		return Cgroup{}, errors.Wrapf(err, "create cgroup")
	}
	log.L.Infof("create cgroup (v1) successful, state: %v", controller.State())

	return Cgroup{
		controller: controller,
	}, nil
}

func (cg Cgroup) Delete() error {
	processes, err := cg.controller.Processes(cgroup1.Memory, true)
	if err != nil {
		return err
	}
	if len(processes) > 0 {
		log.L.Infof("skip destroy cgroup because of running daemon %v", processes)
		return nil
	}
	return cg.controller.Delete()
}
func (cg Cgroup) AddProc(pid int) error {
	err := cg.controller.AddProc(uint64(pid), cgroup1.Memory)
	if err != nil {
		return err
	}

	log.L.Infof("add process %d to daemon cgroup successful", pid)
	return nil
}
