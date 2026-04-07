/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLongestCommonPrefix(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected string
	}{
		{
			name:     "empty slice",
			input:    []string{},
			expected: "",
		},
		{
			name:     "single element",
			input:    []string{"/var/lib/snapshots/1/fs"},
			expected: "/var/lib/snapshots/1/fs",
		},
		{
			name:     "common prefix across snapshot ids",
			input:    []string{"/var/lib/snapshots/128/fs", "/var/lib/snapshots/21/fs", "/var/lib/snapshots/99/fs"},
			expected: "/var/lib/snapshots/",
		},
		{
			name:     "no common prefix",
			input:    []string{"/foo/bar", "/baz/qux"},
			expected: "/",
		},
		{
			name:     "identical strings",
			input:    []string{"/a/b/c", "/a/b/c"},
			expected: "/a/b/c",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := longestCommonPrefix(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestCompactLowerdirOption(t *testing.T) {
	tests := []struct {
		name            string
		options         []string
		expectedChdir   string
		expectedOptions []string
	}{
		{
			name:            "no lowerdir option",
			options:         []string{"ro", "dev"},
			expectedChdir:   "",
			expectedOptions: []string{"ro", "dev"},
		},
		{
			name:            "single lowerdir - no compaction needed",
			options:         []string{"lowerdir=/var/lib/snapshots/1/fs"},
			expectedChdir:   "",
			expectedOptions: []string{"lowerdir=/var/lib/snapshots/1/fs"},
		},
		{
			name: "two lowerdirs with common prefix",
			options: []string{
				"workdir=/var/lib/snapshots/3/work",
				"upperdir=/var/lib/snapshots/3/fs",
				"lowerdir=/var/lib/snapshots/2/fs:/var/lib/snapshots/1/fs",
			},
			expectedChdir: "/var/lib/snapshots/",
			expectedOptions: []string{
				"workdir=3/work",
				"upperdir=3/fs",
				"lowerdir=2/fs:1/fs",
			},
		},
		{
			name: "many lowerdirs simulating real nydus case",
			options: func() []string {
				lowerdirs := make([]string, 0, 108)
				for i := 128; i >= 21; i-- {
					lowerdirs = append(lowerdirs, fmt.Sprintf("/var/lib/nydus-snapshotter/snapshots/%d/fs", i))
				}
				return []string{
					"workdir=/var/lib/nydus-snapshotter/snapshots/129/work",
					"upperdir=/var/lib/nydus-snapshotter/snapshots/129/fs",
					fmt.Sprintf("lowerdir=%s", strings.Join(lowerdirs, ":")),
					"ro",
				}
			}(),
			expectedChdir: "/var/lib/nydus-snapshotter/snapshots/",
			expectedOptions: func() []string {
				lowerdirs := make([]string, 0, 108)
				for i := 128; i >= 21; i-- {
					lowerdirs = append(lowerdirs, fmt.Sprintf("%d/fs", i))
				}
				return []string{
					"workdir=129/work",
					"upperdir=129/fs",
					fmt.Sprintf("lowerdir=%s", strings.Join(lowerdirs, ":")),
					"ro",
				}
			}(),
		},
		{
			name: "disjoint paths - no compaction possible",
			options: []string{
				"workdir=/tmp/work",
				"upperdir=/tmp/upper",
				"lowerdir=/var/lib/snapshots/2/fs:/var/lib/snapshots/1/fs",
			},
			expectedChdir:   "",
			expectedOptions: nil, // same as input
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chdir, newopts := compactLowerdirOption(tc.options)
			assert.Equal(t, tc.expectedChdir, chdir)
			if tc.expectedOptions != nil {
				assert.Equal(t, tc.expectedOptions, newopts)
			}

			if chdir != "" {
				_, newdata := parseOptions(newopts)
				assert.Less(t, len(newdata), len(strings.Join(tc.options, ",")),
					"compacted options should be shorter than original")
			}
		})
	}
}

func TestCompactLowerdirOption_RealWorldSizeSavings(t *testing.T) {
	lowerdirs := make([]string, 0, 108)
	for i := 128; i >= 21; i-- {
		lowerdirs = append(lowerdirs, fmt.Sprintf("/var/lib/nydus-snapshotter/snapshots/%d/fs", i))
	}
	options := []string{
		"workdir=/var/lib/nydus-snapshotter/snapshots/129/work",
		"upperdir=/var/lib/nydus-snapshotter/snapshots/129/fs",
		fmt.Sprintf("lowerdir=%s", strings.Join(lowerdirs, ":")),
	}

	_, origdata := parseOptions(options)
	chdir, newopts := compactLowerdirOption(options)
	require.NotEmpty(t, chdir)

	_, compactdata := parseOptions(newopts)

	assert.Greater(t, len(origdata), 4096, "original data should exceed page size")
	assert.Less(t, len(compactdata), 4096, "compacted data should fit within page size")
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		expectedType    string
		expectedTarget  string
		expectedOptions []string
		expectErr       bool
	}{
		{
			name:            "basic overlay",
			args:            []string{"overlay", "/tmp/merged", "-o", "lowerdir=/a:/b,upperdir=/c,workdir=/d"},
			expectedType:    "overlay",
			expectedTarget:  "/tmp/merged",
			expectedOptions: []string{"lowerdir=/a:/b", "upperdir=/c", "workdir=/d"},
		},
		{
			name:            "filters extraoption",
			args:            []string{"overlay", "/tmp/merged", "-o", "lowerdir=/a:/b,extraoption=abc123,ro"},
			expectedType:    "overlay",
			expectedTarget:  "/tmp/merged",
			expectedOptions: []string{"lowerdir=/a:/b", "ro"},
		},
		{
			name:            "filters kata volume option",
			args:            []string{"overlay", "/tmp/merged", "-o", "lowerdir=/a:/b,io.katacontainers.volume=abc123,ro"},
			expectedType:    "overlay",
			expectedTarget:  "/tmp/merged",
			expectedOptions: []string{"lowerdir=/a:/b", "ro"},
		},
		{
			name:      "invalid fs type",
			args:      []string{"ext4", "/tmp/merged", "-o", "lowerdir=/a"},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			margs, err := parseArgs(tc.args)
			if tc.expectErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expectedType, margs.fsType)
			assert.Equal(t, tc.expectedTarget, margs.target)
			assert.Equal(t, tc.expectedOptions, margs.options)
		})
	}
}
