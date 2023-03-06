/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package config

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/nydus-snapshotter/internal/flags"
	"github.com/stretchr/testify/assert"
)

func TestLoadSnapshotterTOMLConfig(t *testing.T) {
	A := assert.New(t)

	cfg, err := LoadSnapshotterConfig("../misc/snapshotter/config.toml")
	A.NoError(err)

	exampleConfig := SnapshotterConfig{
		Version:        1,
		Root:           "/var/lib/containerd-nydus",
		Address:        "/run/containerd-nydus/containerd-nydus-grpc.sock",
		DaemonMode:     "multiple",
		Experimental:   Experimental{EnableStargz: false},
		CleanupOnClose: false,
		SystemControllerConfig: SystemControllerConfig{
			Enable:  true,
			Address: "/var/run/containerd-nydus/system.sock",
			DebugConfig: DebugConfig{
				ProfileDuration: 5,
				PprofAddress:    "",
			},
		},
		DaemonConfig: DaemonConfig{
			NydusdPath:       "/usr/local/bin/nydusd",
			NydusImagePath:   "/usr/local/bin/nydus-image",
			FsDriver:         "fusedev",
			RecoverPolicy:    "restart",
			NydusdConfigPath: "/etc/nydus/nydusd-config.fusedev.json",
			ThreadsNumber:    4,
		},
		SnapshotsConfig: SnapshotConfig{
			EnableNydusOverlayFS: false,
			SyncRemove:           false,
		},
		RemoteConfig: RemoteConfig{
			ConvertVpcRegistry: false,
			AuthConfig: AuthConfig{
				EnableKubeconfigKeychain: false,
				KubeconfigPath:           "",
			},
			MirrorsConfig: MirrorsConfig{
				Dir: "/etc/nydus/certs.d",
			},
		},
		ImageConfig: ImageConfig{
			PublicKeyFile:     "",
			ValidateSignature: false,
		},
		CacheManagerConfig: CacheManagerConfig{
			Disable:  false,
			GCPeriod: "24h",
			CacheDir: "",
		},
		LoggingConfig: LoggingConfig{
			LogLevel:            "info",
			RotateLogCompress:   true,
			RotateLogLocalTime:  true,
			RotateLogMaxAge:     7,
			RotateLogMaxBackups: 5,
			RotateLogMaxSize:    1,
			LogToStdout:         false,
		},
		MetricsConfig: MetricsConfig{
			Address: ":9110",
		},
	}

	A.EqualValues(cfg, &exampleConfig)

	var args = flags.Args{}
	args.RootDir = "/var/lib/containerd/nydus"
	exampleConfig.Root = "/var/lib/containerd/nydus"
	err = ParseParameters(&args, cfg)
	A.NoError(err)
	A.EqualValues(cfg, &exampleConfig)

	A.EqualValues(cfg.LoggingConfig.LogToStdout, false)

	args.LogToStdout = true
	args.LogToStdoutCount = 1
	err = ParseParameters(&args, cfg)
	A.NoError(err)
	A.EqualValues(cfg.LoggingConfig.LogToStdout, true)

	err = ProcessConfigurations(cfg)
	A.NoError(err)

	A.Equal(GetCacheGCPeriod(), time.Hour*24)
}

func TestSnapshotterConfig(t *testing.T) {
	A := assert.New(t)

	var cfg SnapshotterConfig
	var args flags.Args

	// The log_to_stdout is false in toml file without --log-to-stdout flag.
	// Expected false.
	cfg.LoggingConfig.LogToStdout = false
	args.LogToStdoutCount = 0
	err := ParseParameters(&args, &cfg)
	A.NoError(err)
	A.EqualValues(cfg.LoggingConfig.LogToStdout, false)

	// The log_to_stdout is true in toml file without --log-to-stdout flag.
	// Expected true.
	// This case is failed.
	cfg.LoggingConfig.LogToStdout = true
	args.LogToStdoutCount = 0
	err = ParseParameters(&args, &cfg)
	A.NoError(err)
	A.EqualValues(cfg.LoggingConfig.LogToStdout, true)

	// The log_to_stdout is false in toml file with --log-to-stdout=true.
	// Expected true (command flag has higher priority).
	args.LogToStdout = true
	args.LogToStdoutCount = 1
	cfg.LoggingConfig.LogToStdout = false
	err = ParseParameters(&args, &cfg)
	A.NoError(err)
	A.EqualValues(cfg.LoggingConfig.LogToStdout, true)

	// The log_to_stdout is true in toml file with --log-to-stdout=true.
	// Expected true (command flag has higher priority).
	args.LogToStdout = true
	args.LogToStdoutCount = 1
	cfg.LoggingConfig.LogToStdout = true
	err = ParseParameters(&args, &cfg)
	A.NoError(err)
	A.EqualValues(cfg.LoggingConfig.LogToStdout, true)

	// The log_to_stdout is false in toml file with --log-to-stdout=false.
	// Expected false (command flag has higher priority).
	args.LogToStdout = false
	args.LogToStdoutCount = 1
	cfg.LoggingConfig.LogToStdout = false
	err = ParseParameters(&args, &cfg)
	A.NoError(err)
	A.EqualValues(cfg.LoggingConfig.LogToStdout, false)

	// The log_to_stdout is true in toml file with --log-to-stdout=false.
	// Expected false (command flag has higher priority).
	args.LogToStdout = false
	args.LogToStdoutCount = 1
	cfg.LoggingConfig.LogToStdout = true
	err = ParseParameters(&args, &cfg)
	A.NoError(err)
	A.EqualValues(cfg.LoggingConfig.LogToStdout, false)
}

func TestMergeConfig(t *testing.T) {
	A := assert.New(t)
	var defaultSnapshotterConfig SnapshotterConfig
	var snapshotterConfig1 SnapshotterConfig

	err := defaultSnapshotterConfig.FillUpWithDefaults()
	A.NoError(err)

	err = MergeConfig(&snapshotterConfig1, &defaultSnapshotterConfig)
	A.NoError(err)
	A.Equal(snapshotterConfig1.Root, defaultRootDir)
	A.Equal(snapshotterConfig1.LoggingConfig.LogDir, "")
	A.Equal(snapshotterConfig1.CacheManagerConfig.CacheDir, "")

	A.Equal(snapshotterConfig1.DaemonMode, DefaultDaemonMode)
	A.Equal(snapshotterConfig1.SystemControllerConfig.Address, defaultSystemControllerAddress)
	A.Equal(snapshotterConfig1.LoggingConfig.LogLevel, DefaultLogLevel)
	A.Equal(snapshotterConfig1.LoggingConfig.RotateLogMaxSize, defaultRotateLogMaxSize)
	A.Equal(snapshotterConfig1.LoggingConfig.RotateLogMaxBackups, defaultRotateLogMaxBackups)
	A.Equal(snapshotterConfig1.LoggingConfig.RotateLogMaxAge, defaultRotateLogMaxAge)
	A.Equal(snapshotterConfig1.LoggingConfig.RotateLogCompress, defaultRotateLogCompress)

	A.Equal(snapshotterConfig1.DaemonConfig.NydusdConfigPath, defaultNydusDaemonConfigPath)
	A.Equal(snapshotterConfig1.DaemonConfig.RecoverPolicy, RecoverPolicyRestart.String())
	A.Equal(snapshotterConfig1.CacheManagerConfig.GCPeriod, defaultGCPeriod)

	var snapshotterConfig2 SnapshotterConfig
	snapshotterConfig2.Root = "/snapshotter/root"

	err = MergeConfig(&snapshotterConfig2, &defaultSnapshotterConfig)
	A.NoError(err)
	A.Equal(snapshotterConfig2.Root, "/snapshotter/root")
	A.Equal(snapshotterConfig2.LoggingConfig.LogDir, "")
	A.Equal(snapshotterConfig2.CacheManagerConfig.CacheDir, "")
}

func TestProcessConfigurations(t *testing.T) {
	A := assert.New(t)
	var defaultSnapshotterConfig SnapshotterConfig
	var snapshotterConfig1 SnapshotterConfig

	err := defaultSnapshotterConfig.FillUpWithDefaults()
	A.NoError(err)
	err = MergeConfig(&snapshotterConfig1, &defaultSnapshotterConfig)
	A.NoError(err)
	err = ValidateConfig(&snapshotterConfig1)
	A.NoError(err)
	err = ProcessConfigurations(&snapshotterConfig1)
	A.NoError(err)

	A.Equal(snapshotterConfig1.LoggingConfig.LogDir, filepath.Join(snapshotterConfig1.Root, "logs"))
	A.Equal(snapshotterConfig1.CacheManagerConfig.CacheDir, filepath.Join(snapshotterConfig1.Root, "cache"))

	var snapshotterConfig2 SnapshotterConfig
	snapshotterConfig2.Root = "/snapshotter/root"

	err = MergeConfig(&snapshotterConfig2, &defaultSnapshotterConfig)
	A.NoError(err)
	err = ValidateConfig(&snapshotterConfig2)
	A.NoError(err)
	err = ProcessConfigurations(&snapshotterConfig2)
	A.NoError(err)

	A.Equal(snapshotterConfig2.LoggingConfig.LogDir, filepath.Join(snapshotterConfig2.Root, "logs"))
	A.Equal(snapshotterConfig2.CacheManagerConfig.CacheDir, filepath.Join(snapshotterConfig2.Root, "cache"))
}
