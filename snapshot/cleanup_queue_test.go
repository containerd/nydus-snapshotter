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
)

func TestSnapshotCleanupQueueResumesDeferredSnapshot(t *testing.T) {
	cleaned := make(chan string, 2)
	queue := newSnapshotCleanupQueue(func(_ context.Context, dir string) error {
		cleaned <- dir
		return nil
	})
	defer queue.close()

	queue.deferSnapshot("42")
	if done := queue.enqueueTask("/var/lib/nydus/snapshots/42", "cleanup"); done != nil {
		t.Fatal("ordinary cleanup must stay deferred")
	}
	if !queue.resume("/var/lib/nydus/snapshots/42", "fscache-cull-complete") {
		t.Fatal("completed fscache cull must release deferred cleanup")
	}
	select {
	case got := <-cleaned:
		if got != "/var/lib/nydus/snapshots/42" {
			t.Fatalf("unexpected cleanup directory %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("resumed cleanup did not run")
	}

	done := queue.enqueueTask("/var/lib/nydus/snapshots/42", "cleanup")
	if done == nil {
		t.Fatal("ordinary cleanup must be allowed after resume")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ordinary cleanup did not finish after resume")
	}
}

func TestSnapshotCleanupQueueResumeWaitsForPendingTask(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	queue := newSnapshotCleanupQueue(func(context.Context, string) error {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return nil
	})
	defer queue.close()

	dir := "/var/lib/nydus/snapshots/42"
	done := queue.enqueueTask(dir, "remove")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("initial cleanup did not start")
	}
	queue.deferSnapshot("42")
	if queue.resume(dir, "fscache-cull-complete") {
		t.Fatal("resume must wait until the pending cleanup task exits")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("initial cleanup did not finish")
	}
	if !queue.resume(dir, "fscache-cull-complete") {
		t.Fatal("resume must succeed after the pending task exits")
	}
}
