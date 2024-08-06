/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package cgroup

import (
	"errors"

	"github.com/containerd/cgroups/v3"
	v1 "github.com/containerd/nydus-snapshotter/pkg/cgroup/v1"
	v2 "github.com/containerd/nydus-snapshotter/pkg/cgroup/v2"
)

const (
	defaultSlice = "system.slice"
)

var (
	ErrCgroupNotSupported = errors.New("cgroups: cgroup not supported")
)

type Config struct {
	MemoryLimitInBytes int64
}

type DaemonCgroup interface {
	// Delete the current cgroup.
	Delete() error
	// Add a process to current cgroup.
	AddProc(pid int) error
}

func createCgroup(name string, config Config) (DaemonCgroup, error) {
	if cgroups.Mode() == cgroups.Unified {
		return v2.NewCgroup(defaultSlice, name, config.MemoryLimitInBytes)
	}

	return v1.NewCgroup(defaultSlice, name, config.MemoryLimitInBytes)
}

func supported() bool {
	return cgroups.Mode() != cgroups.Unavailable
}

func displayMode() string {
	switch cgroups.Mode() {
	case cgroups.Legacy:
		return "legacy"
	case cgroups.Hybrid:
		return "hybrid"
	case cgroups.Unified:
		return "unified"
	case cgroups.Unavailable:
		return "unavailable"
	default:
		return "unknown"
	}
}
