/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"context"
	"sync"
	"time"

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/data"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"
)

// renewalStore is the global credential store. It is nil when credential
// renewal is disabled (the default).
var renewalStore *credentialStore

// InitCredentialRenewal initializes the credential renewal subsystem.
// It creates the global store, runs an initial reconciliation pass to seed
// entries from live RAFS instances, and starts a background goroutine that
// reconciles and renews credentials at the given interval.
func InitCredentialRenewal(ctx context.Context, interval time.Duration) {
	renewalStore = newCredentialStore(2 * interval)
	// Reconcile existing credentials a first time.
	renewalStore.reconcile(interval)

	log.G(ctx).WithField("interval", interval).Info("credential renewal initialized")
	go renewLoop(ctx, interval, renewalStore)
}

// renewLoop is the background goroutine that periodically reconciles and
// renews credentials.
func renewLoop(ctx context.Context, interval time.Duration, store *credentialStore) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			store.reconcile(interval)
		}
	}
}

// reconcile reconciles the credential store against the live RAFS instance
// list and renews all entries that should be active.
// 4 different situations are possible:
// - entry in store, live in RAFS: renew
// - entry in store, not live in RAFS: evict
// - entry not in store, live in RAFS: add
// - entry not in store, not live in RAFS: nothing
func (s *credentialStore) reconcile(interval time.Duration) {
	live := make(map[string]any)
	for _, r := range rafs.RafsGlobalCache.List() {
		if r.ImageID != "" {
			live[r.ImageID] = nil
		}
	}

	for _, entry := range s.Entries() {
		if _, inRAFS := live[entry.ref]; inRAFS {
			renewEntry(entry.ref)
		} else if time.Since(entry.renewedAt) >= interval/2 {
			s.Remove(entry.ref)
		}
		// Grace period: the entry was added recently (within interval/2) but
		// has no RAFS entry yet. There is a possible race between a concurrent
		// image pull and this renewal tick: GetRegistryKeyChain adds the ref to the
		// store on the first layer fetch, but the RAFS entry is only created later
		// when the mount completes. Evicting here would cause redundant provider
		// lookups for every remaining layer fetch in the pull. We leave the
		// entry intact; the next tick will either find it in RAFS (normal) or
		// evict it (pull abandoned or failed).
	}

	for ref := range live {
		if s.Get(ref) == nil {
			renewEntry(ref)
		}
	}
}

// renewEntry fetches fresh credentials for ref from the renewable provider
// list. fetchFromProviders writes to the renewal store on success, so
// renewEntry only needs to update metrics.
func renewEntry(ref string) {
	kc := fetchFromProviders(&AuthRequest{Ref: ref}, renewableProviders())
	if kc != nil {
		data.CredentialRenewals.WithLabelValues(ref, "success").Inc()
	} else {
		log.L.WithField("ref", ref).Warn("credential renewal returned no credentials from any provider")
		data.CredentialRenewals.WithLabelValues(ref, "failure").Inc()
	}
}

// --- credentialEntry ---

// credentialEntry holds a cached credential and the time it was last
// successfully renewed.
type credentialEntry struct {
	ref       string
	keychain  *PassKeyChain
	renewedAt time.Time
}

// --- credentialStore ---

// credentialStore is a concurrency-safe in-memory store for renewable
// credentials keyed by image ref.
type credentialStore struct {
	mu       sync.RWMutex
	entries  map[string]*credentialEntry
	lifetime time.Duration // how long an entry is considered valid
}

func newCredentialStore(lifetime time.Duration) *credentialStore {
	return &credentialStore{
		entries:  make(map[string]*credentialEntry),
		lifetime: lifetime,
	}
}

// Add inserts or updates a credential entry for the given ref.
func (s *credentialStore) Add(ref string, kc *PassKeyChain) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.L.WithField("ref", ref).Debug("adding credential entry to store")
	s.entries[ref] = &credentialEntry{
		ref:       ref,
		keychain:  kc,
		renewedAt: time.Now(),
	}
	data.CredentialStoreEntries.WithLabelValues(ref).Set(1)
}

// Get returns the cached keychain for ref, or nil if missing or expired.
func (s *credentialStore) Get(ref string) *PassKeyChain {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.entries[ref]
	if !ok {
		return nil
	}
	if time.Since(entry.renewedAt) > s.lifetime {
		return nil
	}
	return entry.keychain
}

// Remove deletes the entry for the given ref.
func (s *credentialStore) Remove(ref string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.L.WithField("ref", ref).Debug("removing credential entry from store")
	delete(s.entries, ref)
	data.CredentialStoreEntries.WithLabelValues(ref).Set(0)
}

// Entries returns a snapshot of all current entries.
func (s *credentialStore) Entries() []credentialEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]credentialEntry, 0, len(s.entries))
	for _, e := range s.entries {
		result = append(result, *e)
	}
	return result
}
