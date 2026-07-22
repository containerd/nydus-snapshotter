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

type snapshotCleanupTask struct {
	dir        string
	key        string
	snapshotID string
	done       chan struct{}
}

type snapshotCleanupQueue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	tasks   []snapshotCleanupTask
	head    int
	pending map[string]chan struct{}
	closed  bool
	cleanup func(context.Context, string) error
}

func newSnapshotCleanupQueue(cleanup func(context.Context, string) error) *snapshotCleanupQueue {
	q := &snapshotCleanupQueue{
		pending: make(map[string]chan struct{}),
		cleanup: cleanup,
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

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
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
		log.L.Warnf("snapshot cleanup queue is closed snapshot_id=%s dir=%s key=%s",
			task.snapshotID, task.dir, task.key)
		return nil
	}

	if done, ok := q.pending[task.snapshotID]; ok {
		log.L.Debugf("snapshot cleanup task already pending snapshot_id=%s dir=%s key=%s",
			task.snapshotID, task.dir, task.key)
		return done
	}

	q.pending[task.snapshotID] = task.done
	q.tasks = append(q.tasks, task)
	log.L.Infof("queued async snapshot cleanup snapshot_id=%s dir=%s key=%s queue_depth=%d",
		task.snapshotID, task.dir, task.key, len(q.tasks)-q.head)
	q.cond.Signal()
	return task.done
}

func (q *snapshotCleanupQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if !q.closed {
		q.closed = true
		for i := q.head; i < len(q.tasks); i++ {
			task := q.tasks[i]
			delete(q.pending, task.snapshotID)
			close(task.done)
		}
		q.tasks = nil
		q.head = 0
		q.cond.Broadcast()
	}
}

func (q *snapshotCleanupQueue) run() {
	for {
		task, ok := q.next()
		if !ok {
			return
		}
		if q.isClosed() {
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
	log.L.Infof("async snapshot cleanup begin snapshot_id=%s dir=%s key=%s",
		task.snapshotID, task.dir, task.key)

	if err := q.cleanup(context.Background(), task.dir); err != nil {
		log.L.WithError(err).Warnf("async snapshot cleanup failed snapshot_id=%s dir=%s key=%s duration=%s",
			task.snapshotID, task.dir, task.key, time.Since(start))
		return
	}

	log.L.Infof("async snapshot cleanup done snapshot_id=%s dir=%s key=%s duration=%s",
		task.snapshotID, task.dir, task.key, time.Since(start))
}

func (q *snapshotCleanupQueue) finish(task snapshotCleanupTask) {
	q.mu.Lock()
	delete(q.pending, task.snapshotID)
	q.mu.Unlock()
	close(task.done)
}
