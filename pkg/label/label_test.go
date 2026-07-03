/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package label

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseIDMapping(t *testing.T) {
	tests := []struct {
		name        string
		labels      map[string]string
		wantMapping *IDMapping
		wantErr     bool
		errContains string
	}{
		// ---- absent / empty ----
		{
			name:    "no labels",
			labels:  nil,
			wantErr: false,
		},
		{
			name:    "empty labels",
			labels:  map[string]string{},
			wantErr: false,
		},

		// ---- valid ----
		{
			name: "valid idmap",
			labels: map[string]string{
				SnapshotUIDMapping: "0:100000:65536",
				SnapshotGIDMapping: "0:100000:65536",
			},
			wantMapping: &IDMapping{Internal: 0, External: 100000, Range: 65536},
		},
		{
			name: "single id mapping (uid 1)",
			labels: map[string]string{
				SnapshotUIDMapping: "1:1:1",
				SnapshotGIDMapping: "1:1:1",
			},
			wantMapping: &IDMapping{Internal: 1, External: 1, Range: 1},
		},
		{
			name: "trimmed values",
			labels: map[string]string{
				SnapshotUIDMapping: " 0:100000:65536 ",
				SnapshotGIDMapping: " 0:100000:65536 ",
			},
			wantMapping: &IDMapping{Internal: 0, External: 100000, Range: 65536},
		},
		{
			name: "MaxUint32 boundary - fits exactly",
			labels: map[string]string{
				SnapshotUIDMapping: "4294967294:0:1",
				SnapshotGIDMapping: "4294967294:0:1",
			},
			wantMapping: &IDMapping{Internal: 4294967294, External: 0, Range: 1},
		},

		// ---- uid / gid mismatch ----
		{
			name: "uid and gid mismatch",
			labels: map[string]string{
				SnapshotUIDMapping: "0:100000:65536",
				SnapshotGIDMapping: "0:200000:65536",
			},
			wantErr:     true,
			errContains: "do not match",
		},

		// ---- missing one mapping ----
		{
			name: "only uid mapping, missing gid",
			labels: map[string]string{
				SnapshotUIDMapping: "0:100000:65536",
			},
			wantErr:     true,
			errContains: "both uid and gid mappings must be provided",
		},
		{
			name: "only gid mapping, missing uid",
			labels: map[string]string{
				SnapshotGIDMapping: "0:100000:65536",
			},
			wantErr:     true,
			errContains: "both uid and gid mappings must be provided",
		},

		// ---- invalid format ----
		{
			name: "invalid format - missing colon",
			labels: map[string]string{
				SnapshotUIDMapping: "0:100000",
				SnapshotGIDMapping: "0:100000",
			},
			wantErr:     true,
			errContains: "invalid",
		},
		{
			name: "invalid format - extra colons",
			labels: map[string]string{
				SnapshotUIDMapping: "0:100000:65536:extra",
				SnapshotGIDMapping: "0:100000:65536:extra",
			},
			wantErr:     true,
			errContains: "invalid",
		},
		{
			name: "invalid format - non-numeric",
			labels: map[string]string{
				SnapshotUIDMapping: "abc:def:ghi",
				SnapshotGIDMapping: "abc:def:ghi",
			},
			wantErr:     true,
			errContains: "invalid",
		},
		{
			name: "invalid format - negative number",
			labels: map[string]string{
				SnapshotUIDMapping: "-1:100000:65536",
				SnapshotGIDMapping: "-1:100000:65536",
			},
			wantErr:     true,
			errContains: "invalid",
		},
		{
			name: "invalid format - empty middle field",
			labels: map[string]string{
				SnapshotUIDMapping: "0::65536",
				SnapshotGIDMapping: "0::65536",
			},
			wantErr:     true,
			errContains: "invalid",
		},

		// ---- zero range ----
		{
			name: "zero range",
			labels: map[string]string{
				SnapshotUIDMapping: "0:100000:0",
				SnapshotGIDMapping: "0:100000:0",
			},
			wantErr:     true,
			errContains: "zero range",
		},

		// ---- overflow ----
		{
			name: "overflow internal plus range",
			labels: map[string]string{
				SnapshotUIDMapping: "3000000000:0:3000000000",
				SnapshotGIDMapping: "3000000000:0:3000000000",
			},
			wantErr:     true,
			errContains: "overflows",
		},
		{
			name: "overflow external plus range",
			labels: map[string]string{
				SnapshotUIDMapping: "0:3000000000:3000000000",
				SnapshotGIDMapping: "0:3000000000:3000000000",
			},
			wantErr:     true,
			errContains: "overflows",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idMapping, err := ParseIDMapping(tt.labels)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)

			if tt.wantMapping != nil {
				require.NotNil(t, idMapping)
				assert.Equal(t, *tt.wantMapping, *idMapping)
			} else {
				assert.Nil(t, idMapping)
			}
		})
	}
}

func TestRafsInstanceID(t *testing.T) {
	assert.Equal(t, "snapshot-1", RafsInstanceID("snapshot-1", nil))
	assert.Equal(t, "snapshot-1-userns-100000", RafsInstanceID("snapshot-1", &IDMapping{
		Internal: 0,
		External: 100000,
		Range:    65536,
	}))
}

func TestRafsInstanceIDFromLabels(t *testing.T) {
	id, err := RafsInstanceIDFromLabels("snapshot-1", map[string]string{
		SnapshotUIDMapping: "0:100000:65536",
		SnapshotGIDMapping: "0:100000:65536",
	})
	require.NoError(t, err)
	assert.Equal(t, "snapshot-1-userns-100000", id)

	id, err = RafsInstanceIDFromLabels("snapshot-1", nil)
	require.NoError(t, err)
	assert.Equal(t, "snapshot-1", id)
}
