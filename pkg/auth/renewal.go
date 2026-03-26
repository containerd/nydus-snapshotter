/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"sync"
	"time"

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/data"
)

// renewalStore is the global credential store. It is nil when credential
// renewal is disabled (the default).
var renewalStore *credentialStore

// InitCredentialStore creates the global credential store without starting
// any background goroutine. The caller is responsible for driving renewal
// (e.g., from snapshot/renewal.go).
func InitCredentialStore(interval time.Duration) {
	renewalStore = newCredentialStore(interval)
}

// GetStoredCredential returns the cached keychain for ref from the global
// store, or nil if not present or the store is not initialized.
func GetStoredCredential(ref string) *PassKeyChain {
	if renewalStore == nil {
		return nil
	}
	return renewalStore.Get(ref)
}

// RenewCredential fetches fresh credentials for ref from the renewable
// provider list and caches them in the global store. Returns the keychain
// on success or nil on failure. Emits renewal metrics.
func RenewCredential(ref string) *PassKeyChain {
	kc := fetchFromProviders(
		&AuthRequest{Ref: ref, ValidUntil: time.Now().Add(renewalStore.renewInterval)},
		renewableProviders(),
	)
	if kc != nil {
		data.CredentialRenewals.WithLabelValues(ref, "success").Inc()
	} else {
		log.L.WithField("ref", ref).Warn("credential renewal returned no credentials from any provider")
		data.CredentialRenewals.WithLabelValues(ref, "failure").Inc()
	}
	return kc
}

// EvictStaleCredentials removes store entries whose ref is not present in
// liveRefs. Entries added recently (within interval/2) are kept to avoid
// racing with a concurrent image pull: GetRegistryKeyChain adds the ref to
// the store on the first layer fetch, but the RAFS entry is only created
// later when the mount completes. Evicting here would cause redundant
// provider lookups for every remaining layer fetch in the pull.
func EvictStaleCredentials(liveRefs map[string]struct{}) {
	if renewalStore == nil {
		return
	}
	grace := renewalStore.renewInterval / 2
	for _, entry := range renewalStore.Entries() {
		if _, live := liveRefs[entry.ref]; !live && time.Since(entry.renewedAt) >= grace {
			renewalStore.Remove(entry.ref)
		}
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
	mu            sync.RWMutex
	entries       map[string]*credentialEntry
	renewInterval time.Duration
}

func newCredentialStore(renewInterval time.Duration) *credentialStore {
	return &credentialStore{
		entries:       make(map[string]*credentialEntry),
		renewInterval: renewInterval,
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

// Get returns the cached keychain for ref, or nil if not present.
func (s *credentialStore) Get(ref string) *PassKeyChain {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.entries[ref]
	if !ok {
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
	data.CredentialStoreEntries.DeleteLabelValues(ref)
	data.CredentialRenewals.DeleteLabelValues(ref, "success")
	data.CredentialRenewals.DeleteLabelValues(ref, "failure")
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
