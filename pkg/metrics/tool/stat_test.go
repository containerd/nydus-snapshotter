/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tool

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFindZombie(t *testing.T) {
	s, err := GetProcessRunningState(1)

	assert.NoError(t, err)

	assert.Contains(t, []string{"Ss", "S"}, s)
}
