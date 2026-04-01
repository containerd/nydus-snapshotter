/*
 * Copyright (c) 2025. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package remote

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRetryWithPlainHTTP(t *testing.T) {
	const ref = "docker.io/library/nginx:latest"

	httpsErr := fmt.Errorf("Get https://docker.io/v2/: http: server gave HTTP response to HTTPS client")
	connRefusedErr := fmt.Errorf("Get https://docker.io/v2/: dial tcp connect: connection refused")
	otherErr := fmt.Errorf("some unrelated error")

	tests := []struct {
		name             string
		skipHTTPFallback bool
		err              error
		expected         bool
	}{
		{
			name:             "fallback on HTTPS-to-HTTP error",
			skipHTTPFallback: false,
			err:              httpsErr,
			expected:         true,
		},
		{
			name:             "fallback on connection refused",
			skipHTTPFallback: false,
			err:              connRefusedErr,
			expected:         true,
		},
		{
			name:             "no fallback on unrelated error",
			skipHTTPFallback: false,
			err:              otherErr,
			expected:         false,
		},
		{
			name:             "no fallback when nil error",
			skipHTTPFallback: false,
			err:              nil,
			expected:         false,
		},
		{
			name:             "skip fallback even on HTTPS-to-HTTP error",
			skipHTTPFallback: true,
			err:              httpsErr,
			expected:         false,
		},
		{
			name:             "skip fallback even on connection refused",
			skipHTTPFallback: true,
			err:              connRefusedErr,
			expected:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := &Remote{
				skipHTTPFallback: tt.skipHTTPFallback,
			}
			result := remote.RetryWithPlainHTTP(ref, tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
