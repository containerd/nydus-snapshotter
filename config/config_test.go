/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package config

import (
	"testing"
	"time"

	"github.com/containerd/nydus-snapshotter/cmd/containerd-nydus-grpc/pkg/command"
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

	var args = command.Args{}
	args.RootDir = "/var/lib/containerd/nydus"
	exampleConfig.Root = "/var/lib/containerd/nydus"
	err = ParseParameters(&args, cfg)
	A.NoError(err)
	A.EqualValues(cfg, &exampleConfig)

	A.EqualValues(cfg.LoggingConfig.LogToStdout, false)

	args.LogToStdout = true
	err = ParseParameters(&args, cfg)
	A.NoError(err)
	A.EqualValues(cfg.LoggingConfig.LogToStdout, true)

	err = ProcessConfigurations(cfg)
	A.NoError(err)

	A.Equal(GetCacheGCPeriod(), time.Hour*24)
}
