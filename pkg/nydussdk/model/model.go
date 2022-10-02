/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package model

import "strings"

type BuildTimeInfo struct {
	PackageVer string `json:"package_ver"`
	GitCommit  string `json:"git_commit"`
	BuildTime  string `json:"build_time"`
	Profile    string `json:"profile"`
	Rustc      string `json:"rustc"`
}

type DaemonInfo struct {
	ID      string        `json:"id"`
	Version BuildTimeInfo `json:"version"`
	State   string        `json:"state"`
}

type DaemonState int

const (
	DaemonStateUnknown DaemonState = iota
	DaemonStateInit
	DaemonStateReady
	DaemonStateRunning
)

func (info *DaemonInfo) DaemonState() DaemonState {
	s := info.State
	switch {
	case strings.EqualFold(s, "running"):
		return DaemonStateRunning
	case strings.EqualFold(s, "init"):
		return DaemonStateInit
	case strings.EqualFold(s, "ready"):
		return DaemonStateReady
	default:
		return DaemonStateUnknown
	}
}

func (s DaemonState) String() string {
	switch {
	case s == DaemonStateInit:
		return "INIT"
	case s == DaemonStateReady:
		return "READY"
	case s == DaemonStateRunning:
		return "RUNNING"
	default:
		return "UNKNOWN"
	}
}

type ErrorMessage struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type MountRequest struct {
	FsType string `json:"fs_type"`
	Source string `json:"source"`
	Config string `json:"config"`
}

func NewMountRequest(source, config string) MountRequest {
	return MountRequest{
		FsType: "rafs",
		Source: source,
		Config: config,
	}
}

type FsMetric struct {
	FilesAccountEnabled       bool     `json:"files_account_enabled"`
	AccessPatternEnabled      bool     `json:"access_pattern_enabled"`
	MeasureLatency            bool     `json:"measure_latency"`
	ID                        string   `json:"id"`
	DataRead                  uint64   `json:"data_read"`
	BlockCountRead            []uint64 `json:"block_count_read"`
	FopHits                   []uint64 `json:"fop_hits"`
	FopErrors                 []uint64 `json:"fop_errors"`
	FopCumulativeLatencyTotal []uint64 `json:"fop_cumulative_latency_total"`
	ReadLatencyDist           []uint64 `json:"read_latency_dist"`
	NrOpens                   uint64   `json:"nr_opens"`
	NrMaxOpens                uint64   `json:"nr_max_opens"`
	LastFopTp                 uint64   `json:"last_fop_tp"`
}
