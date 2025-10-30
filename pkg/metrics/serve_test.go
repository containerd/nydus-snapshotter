/*
 * Copyright (c) 2025. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/containerd/nydus-snapshotter/internal/constant"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServer(t *testing.T) {
	tests := []struct {
		name                    string
		collectInterval         time.Duration
		hungIOInterval          time.Duration
		managers                []*manager.Manager
		expectedCollectInterval time.Duration
		expectedHungIOInterval  time.Duration
		expectErr               bool
	}{
		{
			name:                    "defaults when no options provided",
			expectedCollectInterval: constant.DefaultCollectInterval,
			expectedHungIOInterval:  constant.DefaultHungIOInterval,
		},
		{
			name:                    "positive collect interval sets value",
			collectInterval:         5 * time.Minute,
			expectedCollectInterval: 5 * time.Minute,
			expectedHungIOInterval:  constant.DefaultHungIOInterval,
		},
		{
			name:            "negative collect interval returns error",
			collectInterval: -1 * time.Minute,
			hungIOInterval:  0,
			expectErr:       true,
		},
		{
			name:                    "positive hung IO interval sets value",
			collectInterval:         0,
			hungIOInterval:          30 * time.Second,
			expectedCollectInterval: constant.DefaultCollectInterval,
			expectedHungIOInterval:  30 * time.Second,
		},
		{
			name:            "negative hung IO interval returns error",
			collectInterval: 0,
			hungIOInterval:  -5 * time.Second,
			expectErr:       true,
		},
		{
			name:                    "both custom positive intervals",
			collectInterval:         3 * time.Minute,
			hungIOInterval:          20 * time.Second,
			expectedCollectInterval: 3 * time.Minute,
			expectedHungIOInterval:  20 * time.Second,
		},
		{
			name:                    "both negative intervals return error",
			collectInterval:         -2 * time.Minute,
			hungIOInterval:          -10 * time.Second,
			expectedCollectInterval: constant.DefaultCollectInterval,
			expectedHungIOInterval:  constant.DefaultHungIOInterval,
			expectErr:               true,
		},
		{
			name:                    "with process manager",
			managers:                []*manager.Manager{&manager.Manager{}},
			expectedCollectInterval: constant.DefaultCollectInterval,
			expectedHungIOInterval:  constant.DefaultHungIOInterval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Build options based on test case
			var opts []ServerOpt
			if tt.managers != nil {
				opts = append(opts, WithProcessManagers(tt.managers))
			}
			if tt.collectInterval != 0 {
				opts = append(opts, WithCollectInterval(tt.collectInterval))
			}
			if tt.hungIOInterval != 0 {
				opts = append(opts, WithHungIOInterval(tt.hungIOInterval))
			}

			srv, err := NewServer(ctx, opts...)

			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, srv)

			// Verify managers
			assert.Equal(t, tt.managers, srv.managers, "managers mismatch")

			// Verify intervals
			assert.Equal(t, tt.expectedCollectInterval, srv.collectInterval, "collect interval mismatch")
			assert.Equal(t, tt.expectedHungIOInterval, srv.hungIOInterval, "hung IO interval mismatch")

			// Verify collectors are always initialized
			assert.NotNil(t, srv.fsCollector, "fsCollector should be initialized")
			assert.NotNil(t, srv.inflightCollector, "inflightCollector should be initialized")
		})
	}
}
