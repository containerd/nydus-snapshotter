/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package config

import (
	"testing"
	"time"

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
		EnableStargz:   false,
		CleanupOnClose: false,
		SystemControllerConfig: SystemControllerConfig{
			Enable:  true,
			Address: "/var/lib/containerd-nydus/system.sock",
			DebugConfig: DebugConfig{
				ProfileDuration: 5,
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

	err = ProcessConfigurations(cfg)
	A.NoError(err)

	A.Equal(GetCacheGCPeriod(), time.Hour*24)
}
