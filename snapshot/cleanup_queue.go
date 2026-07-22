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
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/pkg/errors"
)

type snapshotCleanupTask struct {
	dir        string
	key        string
	snapshotID string
	done       chan struct{}
}

type snapshotCleanupQueue struct {
	mu       sync.Mutex
	cond     *sync.Cond
	tasks    []snapshotCleanupTask
	head     int
	pending  map[string]chan struct{}
	deferred map[string]struct{}
	closed   bool
	cleanup  func(context.Context, string) error
}

func newSnapshotCleanupQueue(cleanup func(context.Context, string) error) *snapshotCleanupQueue {
	q := &snapshotCleanupQueue{
		pending:  make(map[string]chan struct{}),
		deferred: make(map[string]struct{}),
		cleanup:  cleanup,
	}
	q.cond = sync.NewCond(&q.mu)

	go q.run()
	return q
}

func (q *snapshotCleanupQueue) enqueue(dir, key string) {
	q.enqueueTask(dir, key)
}

func (q *snapshotCleanupQueue) enqueueAndWait(ctx context.Context, dir, key string) error {
	done := q.enqueueTask(dir, key)
	if done == nil {
		return nil
	}

	start := time.Now()
	snapshotID := filepath.Base(dir)
	log.L.Debugf("snapshotCleanupQueue enqueueAndWait begin snapshot_id=%s dir=%s key=%s",
		snapshotID, dir, key)
	select {
	case <-done:
		log.L.Debugf("snapshotCleanupQueue enqueueAndWait done snapshot_id=%s dir=%s key=%s duration=%s",
			snapshotID, dir, key, time.Since(start))
		return nil
	case <-ctx.Done():
		err := ctx.Err()
		log.L.Debugf("snapshotCleanupQueue enqueueAndWait done snapshot_id=%s dir=%s key=%s duration=%s error=%v",
			snapshotID, dir, key, time.Since(start), err)
		return err
	}
}

func (q *snapshotCleanupQueue) enqueueTask(dir, key string) <-chan struct{} {
	if dir == "" {
		return nil
	}

	task := snapshotCleanupTask{
		dir:        dir,
		key:        key,
		snapshotID: filepath.Base(dir),
		done:       make(chan struct{}),
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		log.L.Debugf("snapshotCleanupQueue enqueue skipped snapshot_id=%s dir=%s key=%s reason=closed",
			task.snapshotID, task.dir, task.key)
		log.L.Warnf("snapshot cleanup queue is closed snapshot_id=%s dir=%s key=%s",
			task.snapshotID, task.dir, task.key)
		return nil
	}

	if done, ok := q.pending[task.snapshotID]; ok {
		log.L.Debugf("snapshotCleanupQueue enqueue skipped snapshot_id=%s dir=%s key=%s reason=pending queue_depth=%d",
			task.snapshotID, task.dir, task.key, len(q.tasks)-q.head)
		log.L.Debugf("snapshot cleanup task already pending snapshot_id=%s dir=%s key=%s",
			task.snapshotID, task.dir, task.key)
		return done
	}

	if _, ok := q.deferred[task.snapshotID]; ok {
		log.L.Debugf("snapshot cleanup task deferred snapshot_id=%s dir=%s key=%s reason=fscache-cull-pending",
			task.snapshotID, task.dir, task.key)
		return nil
	}

	q.pending[task.snapshotID] = task.done
	q.tasks = append(q.tasks, task)
	log.L.Debugf("queued async snapshot cleanup snapshot_id=%s dir=%s key=%s queue_depth=%d",
		task.snapshotID, task.dir, task.key, len(q.tasks)-q.head)
	q.cond.Signal()
	return task.done
}

func (q *snapshotCleanupQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if !q.closed {
		dropped := len(q.tasks) - q.head
		log.L.Debugf("snapshotCleanupQueue close begin queue_depth=%d pending=%d",
			dropped, len(q.pending))
		q.closed = true
		q.deferred = nil
		for i := q.head; i < len(q.tasks); i++ {
			task := q.tasks[i]
			delete(q.pending, task.snapshotID)
			close(task.done)
		}
		q.tasks = nil
		q.head = 0
		q.cond.Broadcast()
		log.L.Debugf("snapshotCleanupQueue close done dropped=%d pending=%d",
			dropped, len(q.pending))
	}
}

func (q *snapshotCleanupQueue) deferSnapshot(snapshotID string) {
	if snapshotID == "" {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.closed {
		if q.deferred == nil {
			q.deferred = make(map[string]struct{})
		}
		q.deferred[snapshotID] = struct{}{}
	}
}

func (q *snapshotCleanupQueue) clearDeferred(snapshotID string) {
	q.mu.Lock()
	delete(q.deferred, snapshotID)
	q.mu.Unlock()
}

func (q *snapshotCleanupQueue) resume(dir, reason string) bool {
	if dir == "" {
		return false
	}

	task := snapshotCleanupTask{
		dir:        dir,
		key:        reason,
		snapshotID: filepath.Base(dir),
		done:       make(chan struct{}),
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	if _, ok := q.pending[task.snapshotID]; ok {
		return false
	}

	delete(q.deferred, task.snapshotID)
	q.pending[task.snapshotID] = task.done
	q.tasks = append(q.tasks, task)
	q.cond.Signal()
	log.L.Debugf("resumed snapshot cleanup snapshot_id=%s dir=%s reason=%s queue_depth=%d",
		task.snapshotID, task.dir, task.key, len(q.tasks)-q.head)
	return true
}

func (q *snapshotCleanupQueue) run() {
	for {
		task, ok := q.next()
		if !ok {
			log.L.Debug("snapshotCleanupQueue worker exit reason=closed")
			return
		}
		if q.isClosed() {
			log.L.Debugf("snapshotCleanupQueue worker skip task snapshot_id=%s dir=%s key=%s reason=closed",
				task.snapshotID, task.dir, task.key)
			q.finish(task)
			return
		}
		q.runTask(task)
	}
}

func (q *snapshotCleanupQueue) next() (snapshotCleanupTask, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for q.head >= len(q.tasks) && !q.closed {
		q.cond.Wait()
	}
	if q.head >= len(q.tasks) && q.closed {
		return snapshotCleanupTask{}, false
	}

	task := q.tasks[q.head]
	q.tasks[q.head] = snapshotCleanupTask{}
	q.head++
	if q.head == len(q.tasks) {
		q.tasks = nil
		q.head = 0
	}
	return task, true
}

func (q *snapshotCleanupQueue) isClosed() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closed
}

func (q *snapshotCleanupQueue) runTask(task snapshotCleanupTask) {
	defer q.finish(task)

	start := time.Now()
	log.L.Debugf("async snapshot cleanup begin snapshot_id=%s dir=%s key=%s",
		task.snapshotID, task.dir, task.key)

	if err := q.cleanup(context.Background(), task.dir); err != nil {
		log.L.Debugf("snapshotCleanupQueue runTask done snapshot_id=%s dir=%s key=%s duration=%s error=%v",
			task.snapshotID, task.dir, task.key, time.Since(start), err)
		if errors.Is(err, errdefs.ErrFscacheCullPending) {
			return
		}
		log.L.WithError(err).Warnf("async snapshot cleanup failed snapshot_id=%s dir=%s key=%s duration=%s",
			task.snapshotID, task.dir, task.key, time.Since(start))
		return
	}
	log.L.Debugf("async snapshot cleanup done snapshot_id=%s dir=%s key=%s duration=%s",
		task.snapshotID, task.dir, task.key, time.Since(start))
}

func (q *snapshotCleanupQueue) finish(task snapshotCleanupTask) {
	q.mu.Lock()
	delete(q.pending, task.snapshotID)
	q.mu.Unlock()
	close(task.done)
}
