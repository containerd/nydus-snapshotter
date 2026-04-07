/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
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
	const host = "myregistry.example.com"
	ref := fmt.Sprintf("%s/repo/image:latest", host)
	httpResponseErr := fmt.Errorf("Get https://%s/v2/: server gave HTTP response to HTTPS client", host)
	connRefusedErr := fmt.Errorf("Get https://%s/v2/: connect: connection refused", host)
	otherErr := fmt.Errorf("some unrelated error")

	tests := []struct {
		name     string
		insecure bool
		err      error
		want     bool
	}{
		{
			name:     "insecure allows HTTP fallback on HTTP response error",
			insecure: true,
			err:      httpResponseErr,
			want:     true,
		},
		{
			name:     "insecure allows HTTP fallback on connection refused",
			insecure: true,
			err:      connRefusedErr,
			want:     true,
		},
		{
			name:     "insecure does not fallback on unrelated error",
			insecure: true,
			err:      otherErr,
			want:     false,
		},
		{
			name:     "secure blocks HTTP fallback on HTTP response error",
			insecure: false,
			err:      httpResponseErr,
			want:     false,
		},
		{
			name:     "secure blocks HTTP fallback on connection refused",
			insecure: false,
			err:      connRefusedErr,
			want:     false,
		},
		{
			name:     "nil error returns false",
			insecure: true,
			err:      nil,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Remote{insecure: tt.insecure}
			got := r.RetryWithPlainHTTP(ref, tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}
