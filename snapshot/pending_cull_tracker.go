/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/containerd/log"
)

const pendingCullPollInterval = time.Second

type pendingCullCandidate struct {
	dir    string
	blobID string
	reason string
}

type pendingCullTracker struct {
	interval     time.Duration
	pendingCulls func(context.Context) (map[string]string, error)
	queue        *snapshotCleanupQueue

	mu         sync.Mutex
	candidates map[string]pendingCullCandidate
	closed     bool
	cancel     context.CancelFunc
	done       chan struct{}
}

func newPendingCullTracker(
	parent context.Context,
	interval time.Duration,
	pendingCulls func(context.Context) (map[string]string, error),
	queue *snapshotCleanupQueue,
) *pendingCullTracker {
	ctx, cancel := context.WithCancel(parent)
	t := &pendingCullTracker{
		interval:     interval,
		pendingCulls: pendingCulls,
		queue:        queue,
		candidates:   make(map[string]pendingCullCandidate),
		cancel:       cancel,
		done:         make(chan struct{}),
	}
	go t.run(ctx)
	return t
}

func (t *pendingCullTracker) register(dir, blobID, reason string) {
	snapshotID := filepath.Base(dir)
	candidate := pendingCullCandidate{
		dir:    filepath.Clean(dir),
		blobID: blobID,
		reason: reason,
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	previous, existed := t.candidates[snapshotID]
	t.candidates[snapshotID] = candidate
	t.queue.deferSnapshot(snapshotID)
	t.mu.Unlock()

	if !existed || previous.blobID != blobID || previous.reason != reason {
		log.L.Infof("tracking pending fscache cleanup snapshot_id=%s blob_id=%s reason=%s",
			snapshotID, blobID, reason)
	}
}

func (t *pendingCullTracker) forget(snapshotID string) {
	t.mu.Lock()
	delete(t.candidates, snapshotID)
	t.queue.clearDeferred(snapshotID)
	t.mu.Unlock()
}

func (t *pendingCullTracker) close() {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	t.cancel()
	<-t.done
}

func (t *pendingCullTracker) run(ctx context.Context) {
	defer close(t.done)
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if t.candidateCount() == 0 {
				continue
			}
			if err := t.reconcile(ctx); err != nil {
				log.L.WithError(err).Warnf("failed to query pending fscache culls candidates=%d", t.candidateCount())
			}
		}
	}
}

func (t *pendingCullTracker) reconcile(ctx context.Context) error {
	pending, err := t.pendingCulls(ctx)
	if err != nil {
		return err
	}

	t.mu.Lock()
	candidates := make(map[string]pendingCullCandidate, len(t.candidates))
	for snapshotID, candidate := range t.candidates {
		candidates[snapshotID] = candidate
	}
	t.mu.Unlock()

	for snapshotID, candidate := range candidates {
		if reason, ok := pending[candidate.blobID]; ok {
			if reason != candidate.reason {
				t.updateReason(snapshotID, candidate.blobID, reason)
			}
			continue
		}

		t.mu.Lock()
		current, ok := t.candidates[snapshotID]
		if !ok || current.blobID != candidate.blobID {
			t.mu.Unlock()
			continue
		}
		resumed := t.queue.resume(current.dir, "fscache-cull-complete")
		if resumed {
			delete(t.candidates, snapshotID)
		}
		t.mu.Unlock()
		if !resumed {
			continue
		}

		log.L.Infof("resumed pending fscache cleanup snapshot_id=%s blob_id=%s", snapshotID, candidate.blobID)
	}
	return nil
}

func (t *pendingCullTracker) updateReason(snapshotID, blobID, reason string) {
	t.mu.Lock()
	candidate, ok := t.candidates[snapshotID]
	if ok && candidate.blobID == blobID {
		candidate.reason = reason
		t.candidates[snapshotID] = candidate
	}
	t.mu.Unlock()
}

func (t *pendingCullTracker) candidateCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.candidates)
}
