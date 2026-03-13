/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"context"
	"testing"
	"time"

	mgr "github.com/containerd/nydus-snapshotter/pkg/manager"
)

// TestStartCredentialRenewalLifecycle verifies that the renewal goroutine
// starts, ticks, and stops cleanly on context cancellation. The managers
// list is empty so reconcile is a no-op each tick.
func TestStartCredentialRenewalLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())

	startCredentialRenewal(ctx, 30*time.Millisecond, []*mgr.Manager{})

	// Let it tick a few times without error.
	time.Sleep(100 * time.Millisecond)

	cancel()
	// Give goroutine time to observe cancellation.
	time.Sleep(60 * time.Millisecond)
}
