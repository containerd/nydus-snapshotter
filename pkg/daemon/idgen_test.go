/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemon

import (
	"testing"

	"gotest.tools/assert"
)

func TestIdGenerate(t *testing.T) {
	id1 := newID()
	id2 := newID()

	assert.Assert(t, len(id1) > 0)
	assert.Assert(t, len(id2) > 0)
	assert.Assert(t, id1 != id2)
}
