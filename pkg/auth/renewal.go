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
)

// renewalStore is the global credential store. It is nil when credential
// renewal is disabled (the default).
var renewalStore *credentialStore

// RemoveCredentials removes the cached credentials for the given ref.
// Safe to call when renewal is disabled (no-op).
func RemoveCredentials(ref string) {
	if renewalStore != nil {
		renewalStore.Remove(ref)
	}
}

// InitCredentialRenewal initializes the credential renewal subsystem.
// It creates the global store, seeds it from existingRefs by calling
// GetRegistryKeyChain for each, and starts a background goroutine that
// renews all entries at the given interval.
func InitCredentialRenewal(ctx context.Context, interval time.Duration, existingRefs []string) {
	// Use 2× the renewal interval as store lifetime so that one missed tick
	// does not immediately cause credentials to be considered expired.
	renewalStore = newCredentialStore(2 * interval)

	// Seed the store from existing RAFS instances.
	for _, ref := range existingRefs {
		GetRegistryKeyChain(ref, nil)
	}

	log.G(ctx).WithField("interval", interval).
		WithField("seeded", len(existingRefs)).
		Info("credential renewal initialized")

	store := renewalStore
	go renewLoop(ctx, interval, store)
}

// renewLoop is the background goroutine that periodically renews all
// stored credentials.
func renewLoop(ctx context.Context, interval time.Duration, store *credentialStore) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewCredentials(store)
		}
	}
}

// renewCredentials iterates all store entries and re-fetches credentials
// from each entry's provider.
func renewCredentials(store *credentialStore) {
	for _, entry := range store.Entries() {
		req := &AuthRequest{Ref: entry.ref}
		kc, err := entry.provider.GetCredentials(req)
		if err != nil {
			log.L.WithError(err).WithField("ref", entry.ref).
				Warn("Failed to renew credentials")
			data.CredentialRenewals.WithLabelValues(entry.ref, "failure").Inc()
			continue
		}
		if kc == nil {
			continue
		}

		store.updateKeychain(entry.ref, kc)
		data.CredentialRenewals.WithLabelValues(entry.ref, "success").Inc()
	}
}

// --- credentialEntry ---

// credentialEntry holds a cached credential along with the provider
// that produced it, so the renewal goroutine can re-query.
type credentialEntry struct {
	ref      string
	provider AuthProvider
	keychain *PassKeyChain
	// renewedAt tracks when the credentials were last successfully renewed,
	// used by the store to determine expiration.
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
func (s *credentialStore) Add(ref string, provider AuthProvider, kc *PassKeyChain) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.L.WithFields(map[string]any{"ref": ref, "provider": provider}).Debug("adding credential entry to store")
	s.entries[ref] = &credentialEntry{
		ref:       ref,
		provider:  provider,
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

	log.L.WithFields(map[string]any{"ref": ref}).Debug("removing credential entry from store")
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

// updateKeychain updates the keychain for an existing entry.
func (s *credentialStore) updateKeychain(ref string, kc *PassKeyChain) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[ref]
	if !ok {
		return
	}
	log.L.WithFields(map[string]any{"ref": ref, "provider": entry.provider}).Debug("updating credential entry in store")
	entry.keychain = kc
	entry.renewedAt = time.Now()
}
