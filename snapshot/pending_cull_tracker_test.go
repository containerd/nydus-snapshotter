/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPendingCullTrackerResumesAfterNydusdCompletion(t *testing.T) {
	dir := "/var/lib/nydus/snapshots/42"
	cleaned := make(chan string, 1)
	queue := newSnapshotCleanupQueue(func(_ context.Context, got string) error {
		cleaned <- got
		return nil
	})
	defer queue.close()

	pending := map[string]string{"blob": "open"}
	tracker := &pendingCullTracker{
		pendingCulls: func(context.Context) (map[string]string, error) { return pending, nil },
		queue:        queue,
		candidates:   make(map[string]pendingCullCandidate),
	}
	tracker.register(dir, "blob", "awaiting cachefiles commit")

	if err := tracker.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tracker.candidates["42"].reason != "open" {
		t.Fatalf("pending reason was not updated: %+v", tracker.candidates["42"])
	}
	pending = map[string]string{}
	if err := tracker.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-cleaned:
		if got != dir {
			t.Fatalf("unexpected cleanup directory %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup was not resumed after cull completion")
	}
	if len(tracker.candidates) != 0 {
		t.Fatalf("completed candidate was retained: %+v", tracker.candidates)
	}
}

func TestPendingCullTrackerUsesOneBatchQuery(t *testing.T) {
	queue := newSnapshotCleanupQueue(func(context.Context, string) error { return nil })
	defer queue.close()

	queries := 0
	tracker := &pendingCullTracker{
		pendingCulls: func(context.Context) (map[string]string, error) {
			queries++
			return map[string]string{"blob-a": "open", "blob-b": "inuse"}, nil
		},
		queue:      queue,
		candidates: make(map[string]pendingCullCandidate),
	}
	tracker.register("/snapshots/1", "blob-a", "open")
	tracker.register("/snapshots/2", "blob-b", "inuse")

	if err := tracker.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if queries != 1 {
		t.Fatalf("expected one batch query, got %d", queries)
	}
}

func TestPendingCullTrackerRetainsCandidatesOnQueryError(t *testing.T) {
	queue := newSnapshotCleanupQueue(func(context.Context, string) error { return nil })
	defer queue.close()

	tracker := &pendingCullTracker{
		pendingCulls: func(context.Context) (map[string]string, error) {
			return nil, errors.New("nydusd unavailable")
		},
		queue:      queue,
		candidates: make(map[string]pendingCullCandidate),
	}
	tracker.register("/snapshots/1", "blob", "open")

	if err := tracker.reconcile(context.Background()); err == nil {
		t.Fatal("expected pending query error")
	}
	if len(tracker.candidates) != 1 {
		t.Fatalf("candidate was lost after query error: %+v", tracker.candidates)
	}
}
