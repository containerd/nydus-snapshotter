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
	const (
		remoteHost = "myregistry.example.com"
		localHost  = "localhost:5000"
		localIP    = "127.0.0.1:5000"
	)

	remoteRef := fmt.Sprintf("%s/repo/image:latest", remoteHost)
	localRef := fmt.Sprintf("%s/repo/image:latest", localHost)
	localIPRef := fmt.Sprintf("%s/repo/image:latest", localIP)

	remoteHTTPResponseErr := fmt.Errorf("Get https://%s/v2/: server gave HTTP response to HTTPS client", remoteHost)
	remoteConnRefusedErr := fmt.Errorf("Get https://%s/v2/: connect: connection refused", remoteHost)
	localHTTPResponseErr := fmt.Errorf("Get https://%s/v2/: server gave HTTP response to HTTPS client", localHost)
	localIPHTTPResponseErr := fmt.Errorf("Get https://%s/v2/: server gave HTTP response to HTTPS client", localIP)
	otherErr := fmt.Errorf("some unrelated error")

	tests := []struct {
		name     string
		ref      string
		insecure bool
		err      error
		want     bool
	}{
		{
			name:     "insecure allows HTTP fallback on HTTP response error",
			ref:      remoteRef,
			insecure: true,
			err:      remoteHTTPResponseErr,
			want:     true,
		},
		{
			name:     "insecure allows HTTP fallback on connection refused",
			ref:      remoteRef,
			insecure: true,
			err:      remoteConnRefusedErr,
			want:     true,
		},
		{
			name:     "insecure does not fallback on unrelated error",
			ref:      remoteRef,
			insecure: true,
			err:      otherErr,
			want:     false,
		},
		{
			name:     "secure blocks HTTP fallback on HTTP response error",
			ref:      remoteRef,
			insecure: false,
			err:      remoteHTTPResponseErr,
			want:     false,
		},
		{
			name:     "secure blocks HTTP fallback on connection refused",
			ref:      remoteRef,
			insecure: false,
			err:      remoteConnRefusedErr,
			want:     false,
		},
		{
			name:     "nil error returns false",
			ref:      remoteRef,
			insecure: true,
			err:      nil,
			want:     false,
		},
		{
			name:     "secure allows localhost HTTP fallback on HTTP response error",
			ref:      localRef,
			insecure: false,
			err:      localHTTPResponseErr,
			want:     true,
		},
		{
			name:     "secure allows loopback IP HTTP fallback on HTTP response error",
			ref:      localIPRef,
			insecure: false,
			err:      localIPHTTPResponseErr,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Remote{insecure: tt.insecure}
			got := r.RetryWithPlainHTTP(tt.ref, tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}
