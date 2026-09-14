/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tool

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFindZombie(t *testing.T) {
	s, err := GetProcessRunningState(1)

	assert.NoError(t, err)

	assert.Contains(t, []string{"Ss", "S"}, s)
}

func TestGetProcessStartTime(t *testing.T) {
	first, err := GetProcessStartTime(os.Getpid())
	assert.NoError(t, err)
	assert.NotZero(t, first)

	// The start time of a running process never changes.
	second, err := GetProcessStartTime(os.Getpid())
	assert.NoError(t, err)
	assert.Equal(t, first, second)

	_, err = GetProcessStartTime(-1)
	assert.Error(t, err)
}
