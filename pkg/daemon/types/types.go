/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package types

type BuildTimeInfo struct {
	PackageVer string `json:"package_ver"`
	GitCommit  string `json:"git_commit"`
	BuildTime  string `json:"build_time"`
	Profile    string `json:"profile"`
	Rustc      string `json:"rustc"`
}

type DaemonState string

type DaemonInfo struct {
	ID      string        `json:"id"`
	Version BuildTimeInfo `json:"version"`
	State   DaemonState   `json:"state"`
}

const (
	DaemonStateUnknown   DaemonState = "UNKNOWN"
	DaemonStateInit      DaemonState = "INIT"
	DaemonStateReady     DaemonState = "READY"
	DaemonStateRunning   DaemonState = "RUNNING"
	DaemonStateDied      DaemonState = "DIED"
	DaemonStateDestroyed DaemonState = "DESTROYED"
)

func (info *DaemonInfo) DaemonState() DaemonState {
	return info.State
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

type FsMetrics struct {
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

type CacheMetrics struct {
	ID              string   `json:"id"`
	UnderlyingFiles []string `json:"underlying_files"`
	StorePath       string   `json:"store_path"`
	PartialHits     uint64   `json:"partial_hits"`
	WholeHits       uint64   `json:"whole_hits"`
	Total           uint64   `json:"total"`
	EntriesCount    uint32   `json:"entries_count"`
}
