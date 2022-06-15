/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package fs

import (
	"testing"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/stretchr/testify/assert"
)

func TestWithNydusdBinaryPath(t *testing.T) {
	var fs filesystem
	opt := WithNydusdBinaryPath("/bin/nydusd", config.DaemonModeMultiple)
	err := opt(&fs)
	assert.Nil(t, err)
	assert.Equal(t, "/bin/nydusd", fs.nydusdBinaryPath)
}
