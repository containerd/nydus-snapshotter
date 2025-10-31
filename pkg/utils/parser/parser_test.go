/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMemoryLimitToBytes(t *testing.T) {
	totalMemoryBytes := 10000

	for desc, test := range map[string]struct {
		MemoryLimit string
		expected    int64
	}{
		"memory limit is zero": {
			MemoryLimit: "",
			expected:    -1,
		},
		"memory limit is a percentage": {
			MemoryLimit: "20%",
			expected:    2000,
		},
		"memory limit is a float percentage": {
			MemoryLimit: "0.2%",
			expected:    20,
		},
		"memory limit is a value without unit": {
			MemoryLimit: "10240",
			expected:    10240,
		},
		"memory limit is a value with Byte unit": {
			MemoryLimit: "10240B",
			expected:    10240,
		},
		"memory limit is a value with KiB unit": {
			MemoryLimit: "30KiB",
			expected:    30 * 1024,
		},
		"memory limit is a value with MiB unit": {
			MemoryLimit: "30MiB",
			expected:    30 * 1024 * 1024,
		},
		"memory limit is a value with GiB unit": {
			MemoryLimit: "30GiB",
			expected:    30 * 1024 * 1024 * 1024,
		},
		"memory limit is a value with TiB unit": {
			MemoryLimit: "30TiB",
			expected:    30 * 1024 * 1024 * 1024 * 1024,
		},
		"memory limit is a value with PiB unit": {
			MemoryLimit: "30PiB",
			expected:    30 * 1024 * 1024 * 1024 * 1024 * 1024,
		},
		"memory limit is a value with Ki unit": {
			MemoryLimit: "30Ki",
			expected:    30 * 1024,
		},
		"memory limit is a value with Mi unit": {
			MemoryLimit: "30Mi",
			expected:    30 * 1024 * 1024,
		},
		"memory limit is a value with Gi unit": {
			MemoryLimit: "30Gi",
			expected:    30 * 1024 * 1024 * 1024,
		},
		"memory limit is a value with Ti unit": {
			MemoryLimit: "30Ti",
			expected:    30 * 1024 * 1024 * 1024 * 1024,
		},
		"memory limit is a value with Pi unit": {
			MemoryLimit: "30Pi",
			expected:    30 * 1024 * 1024 * 1024 * 1024 * 1024,
		},
	} {
		t.Logf("TestCase %q", desc)

		memoryLimitInBytes, err := MemoryConfigToBytes(test.MemoryLimit, totalMemoryBytes)
		assert.NoError(t, err)
		assert.Equal(t, memoryLimitInBytes, test.expected)
	}
}
