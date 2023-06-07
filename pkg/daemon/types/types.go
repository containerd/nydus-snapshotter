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

const (
	DaemonStateUnknown   DaemonState = "UNKNOWN"
	DaemonStateInit      DaemonState = "INIT"
	DaemonStateReady     DaemonState = "READY"
	DaemonStateRunning   DaemonState = "RUNNING"
	DaemonStateDied      DaemonState = "DIED"
	DaemonStateDestroyed DaemonState = "DESTROYED"
)

type DaemonInfo struct {
	ID      string        `json:"id"`
	Version BuildTimeInfo `json:"version"`
	State   DaemonState   `json:"state"`
}

func (info *DaemonInfo) DaemonState() DaemonState {
	return info.State
}

func (info *DaemonInfo) DaemonVersion() BuildTimeInfo {
	return info.Version
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
}

type InflightMetrics struct {
	Values []struct {
		Inode         uint64 `json:"inode"`
		Opcode        uint32 `json:"opcode"`
		Unique        uint64 `json:"unique"`
		TimestampSecs uint64 `json:"timestamp_secs"`
	}
}

type CacheMetrics struct {
	ID                           string   `json:"id"`
	UnderlyingFiles              []string `json:"underlying_files"`
	StorePath                    string   `json:"store_path"`
	PartialHits                  uint64   `json:"partial_hits"`
	WholeHits                    uint64   `json:"whole_hits"`
	Total                        uint64   `json:"total"`
	EntriesCount                 uint64   `json:"entries_count"`
	PrefetchDataAmount           uint64   `json:"prefetch_data_amount"`
	PrefetchRequestsCount        uint64   `json:"prefetch_requests_count"`
	PrefetchWorkers              uint     `json:"prefetch_workers"`
	PrefetchCumulativeTimeMillis uint64   `json:"prefetch_cumulative_time_millis"`
	PrefetchBeginTimeSecs        uint64   `json:"prefetch_begin_time_secs"`
	PrefetchEndTimeSecs          uint64   `json:"prefetch_end_time_secs"`
	BufferedBackendSize          uint64   `json:"buffered_backend_size"`
}
