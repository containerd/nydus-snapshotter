/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemon

import (
	"testing"

	"github.com/containerd/nydus-snapshotter/config"
	"gotest.tools/assert"
)

func TestConfigOptions(t *testing.T) {
	tmpDir := t.TempDir()
	opts := []NewDaemonOpt{
		WithSocketDir("/tmp/socket"),
		WithRef(5),
		WithLogDir(tmpDir),
		WithLogToStdout(true),
		WithLogLevel("Warning"),
		WithLogRotationSize(1024),
		WithConfigDir(tmpDir),
		WithMountpoint("/tmp/mnt"),
		WithNydusdThreadNum(4),
		WithFsDriver("fscache"),
		WithDaemonMode("dedicated"),
	}

	daemon, err := NewDaemon(opts...)
	assert.Assert(t, err)
	assert.Equal(t, daemon.States.APISocket, "/tmp/socket/"+daemon.ID()+"/api.sock")
	assert.Equal(t, daemon.ref, int32(5))
	assert.Equal(t, daemon.States.LogDir, tmpDir+"/"+daemon.ID())
	assert.Equal(t, daemon.States.LogToStdout, true)
	assert.Equal(t, daemon.States.LogLevel, "Warning")
	assert.Equal(t, daemon.States.LogRotationSize, 1024)
	assert.Equal(t, daemon.States.ConfigDir, tmpDir+"/"+daemon.ID())
	assert.Equal(t, daemon.States.Mountpoint, "/tmp/mnt")
	assert.Equal(t, daemon.States.ThreadNum, 4)
	assert.Equal(t, daemon.States.FsDriver, "fscache")
	assert.Equal(t, string(daemon.States.DaemonMode), "dedicated")

}

func String(daemonMode config.DaemonMode) {
	panic("unimplemented")
}
