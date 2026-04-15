/*
 * Copyright (c) 2025. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseIDMappingHostID(t *testing.T) {
	tests := []struct {
		name      string
		mapping   string
		wantID    int
		wantError bool
	}{
		{
			name:    "valid mapping containerID=0",
			mapping: "0:1000:65536",
			wantID:  1000,
		},
		{
			name:    "host ID zero is valid",
			mapping: "0:0:65536",
			wantID:  0,
		},
		{
			name:    "large host ID",
			mapping: "0:65534:65536",
			wantID:  65534,
		},
		{
			name:    "minimal size of 1",
			mapping: "0:500:1",
			wantID:  500,
		},
		{
			name:      "containerID non-zero is rejected",
			mapping:   "1:1000:65536",
			wantError: true,
		},
		{
			name:      "negative containerID is rejected",
			mapping:   "-1:1000:65536",
			wantError: true,
		},
		{
			name:      "negative hostID is rejected",
			mapping:   "0:-1:65536",
			wantError: true,
		},
		{
			name:      "zero size is rejected",
			mapping:   "0:1000:0",
			wantError: true,
		},
		{
			name:      "negative size is rejected",
			mapping:   "0:1000:-1",
			wantError: true,
		},
		{
			name:      "empty string",
			mapping:   "",
			wantError: true,
		},
		{
			name:      "missing fields",
			mapping:   "0:1000",
			wantError: true,
		},
		{
			name:      "non-numeric characters",
			mapping:   "a:b:c",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := parseIDMappingHostID(tt.mapping)
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantID, id)
			}
		})
	}
}
