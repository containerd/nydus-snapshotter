/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- test helpers ---

// mockProvider implements RenewableProvider.
type mockProvider struct {
	creds *PassKeyChain
	err   error
}

func (m *mockProvider) GetCredentials(_ *AuthRequest) (*PassKeyChain, error) {
	return m.creds, m.err
}

func (m *mockProvider) CanRenew() bool { return true }

func (m *mockProvider) String() string { return "mockProvider" }

// mockNonRenewableProvider implements AuthProvider but NOT RenewableProvider.
type mockNonRenewableProvider struct {
	creds *PassKeyChain
	err   error
}

func (m *mockNonRenewableProvider) GetCredentials(_ *AuthRequest) (*PassKeyChain, error) {
	return m.creds, m.err
}

func (m *mockNonRenewableProvider) String() string { return "mockNonRenewableProvider" }

// trackingProvider counts calls and returns evolving credentials.
type trackingProvider struct {
	calls    atomic.Int32
	failNext atomic.Bool
	nilNext  atomic.Bool
}

func (p *trackingProvider) GetCredentials(_ *AuthRequest) (*PassKeyChain, error) {
	n := p.calls.Add(1)
	if p.failNext.Load() {
		return nil, fmt.Errorf("simulated failure")
	}
	if p.nilNext.Load() {
		return nil, fmt.Errorf("no credentials available")
	}
	return &PassKeyChain{
		Username: fmt.Sprintf("user-v%d", n),
		Password: fmt.Sprintf("pass-v%d", n),
	}, nil
}

func (p *trackingProvider) CanRenew() bool { return true }

func (p *trackingProvider) String() string { return "trackingProvider" }

// --- RenewableProvider type assertion ---

func TestRenewableProviderTypeAssertion(t *testing.T) {
	tests := []struct {
		name      string
		provider  AuthProvider
		renewable bool
	}{
		{"LabelsProvider is not renewable", NewLabelsProvider(), false},
		{"CRIProvider is not renewable", NewCRIProvider(), false},
		{"DockerProvider is renewable", NewDockerProvider(), true},
		{"KubeSecretProvider is renewable", NewKubeSecretProvider(), true},
		{"KubeletProvider is renewable", &KubeletProvider{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp, ok := tt.provider.(RenewableProvider)
			if tt.renewable {
				assert.True(t, ok, "expected provider to implement RenewableProvider")
				assert.True(t, rp.CanRenew(), "expected CanRenew() to return true")
			} else {
				assert.False(t, ok, "expected provider to NOT implement RenewableProvider")
			}
		})
	}
}

// --- credentialStore ---

func TestCredentialStoreGet(t *testing.T) {
	tests := []struct {
		name     string
		lifetime time.Duration
		setup    func(s *credentialStore)
		ref      string
		wantNil  bool
		wantUser string
	}{
		{
			name:     "returns cached entry",
			lifetime: 5 * time.Minute,
			setup: func(s *credentialStore) {
				s.Add("ref", &PassKeyChain{Username: "user", Password: "pass"})
			},
			ref:      "ref",
			wantUser: "user",
		},
		{
			name:     "returns nil for missing ref",
			lifetime: 5 * time.Minute,
			setup:    func(_ *credentialStore) {},
			ref:      "nonexistent",
			wantNil:  true,
		},
		{
			name:     "returns nil for expired entry",
			lifetime: 1 * time.Millisecond,
			setup: func(s *credentialStore) {
				s.Add("ref", &PassKeyChain{Username: "user", Password: "pass"})
				time.Sleep(5 * time.Millisecond)
			},
			ref:     "ref",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newCredentialStore(tt.lifetime)
			tt.setup(store)

			got := store.Get(tt.ref)
			if tt.wantNil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, tt.wantUser, got.Username)
			}
		})
	}
}

func TestCredentialStoreRemove(t *testing.T) {
	tests := []struct {
		name  string
		setup func(s *credentialStore)
		ref   string
	}{
		{
			name: "removes existing entry",
			setup: func(s *credentialStore) {
				s.Add("ref", &PassKeyChain{Username: "user", Password: "pass"})
			},
			ref: "ref",
		},
		{
			name:  "no-op for nonexistent ref",
			setup: func(_ *credentialStore) {},
			ref:   "nonexistent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newCredentialStore(5 * time.Minute)
			tt.setup(store)
			store.Remove(tt.ref)
			assert.Nil(t, store.Get(tt.ref))
		})
	}
}

func TestCredentialStoreAdd_Upsert(t *testing.T) {
	store := newCredentialStore(5 * time.Minute)

	store.Add("ref", &PassKeyChain{Username: "old", Password: "old"})
	got := store.Get("ref")
	require.NotNil(t, got)
	assert.Equal(t, "old", got.Username)

	store.Add("ref", &PassKeyChain{Username: "new", Password: "new"})
	got = store.Get("ref")
	require.NotNil(t, got)
	assert.Equal(t, "new", got.Username)
}

func TestCredentialStoreEntries(t *testing.T) {
	store := newCredentialStore(5 * time.Minute)

	store.Add("ref1", &PassKeyChain{Username: "u1", Password: "p1"})
	store.Add("ref2", &PassKeyChain{Username: "u2", Password: "p2"})

	assert.Len(t, store.Entries(), 2)
}

func TestCredentialStoreConcurrency(t *testing.T) {
	store := newCredentialStore(5 * time.Minute)

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store.Add("ref", &PassKeyChain{Username: "user", Password: "pass"})
			store.Get("ref")
			store.Entries()
			if i%2 == 0 {
				store.Remove("ref")
			}
		}(i)
	}
	wg.Wait()
}

// --- renewEntry ---

func TestRenewEntry(t *testing.T) {
	const ref = "docker.io/library/nginx:latest"

	tests := []struct {
		name     string
		provider *trackingProvider
		wantUser string
		wantCall int32
	}{
		{
			name:     "updates store on success",
			provider: &trackingProvider{},
			wantUser: "user-v1",
			wantCall: 1,
		},
		{
			name: "leaves existing entry unchanged on failure",
			provider: func() *trackingProvider {
				p := &trackingProvider{}
				p.failNext.Store(true)
				return p
			}(),
			wantUser: "original",
			wantCall: 1,
		},
		{
			name: "leaves existing entry unchanged when provider returns nil",
			provider: func() *trackingProvider {
				p := &trackingProvider{}
				p.nilNext.Store(true)
				return p
			}(),
			wantUser: "original",
			wantCall: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldRenewable := renewableProviders
			oldStore := renewalStore
			defer func() {
				renewableProviders = oldRenewable
				renewalStore = oldStore
			}()
			renewableProviders = func() []AuthProvider { return []AuthProvider{tt.provider} }

			store := newCredentialStore(5 * time.Minute)
			store.Add(ref, &PassKeyChain{Username: "original", Password: "original"})
			renewalStore = store

			renewEntry(ref)

			assert.Equal(t, tt.wantCall, tt.provider.calls.Load())
			got := store.Get(ref)
			require.NotNil(t, got)
			assert.Equal(t, tt.wantUser, got.Username)
		})
	}
}

// --- getRegistryKeyChainFromProviders ---

func TestGetRegistryKeyChainFromProviders(t *testing.T) {
	tests := []struct {
		name         string
		storeEnabled bool
		cached       *PassKeyChain
		providers    []AuthProvider
		wantUser     string
		wantStored   bool
	}{
		{
			name:         "serves from store when cached",
			storeEnabled: true,
			cached:       &PassKeyChain{Username: "cached", Password: "cached-pass"},
			providers:    []AuthProvider{&mockNonRenewableProvider{creds: &PassKeyChain{Username: "fresh", Password: "fresh"}}},
			wantUser:     "cached",
			wantStored:   true,
		},
		{
			name:         "falls through on store miss",
			storeEnabled: true,
			providers:    []AuthProvider{&mockProvider{creds: &PassKeyChain{Username: "fresh", Password: "fresh"}}},
			wantUser:     "fresh",
			wantStored:   true,
		},
		{
			name:         "stores renewable provider creds",
			storeEnabled: true,
			providers:    []AuthProvider{&mockProvider{creds: &PassKeyChain{Username: "user", Password: "pass"}}},
			wantUser:     "user",
			wantStored:   true,
		},
		{
			name:         "does not store non-renewable provider creds",
			storeEnabled: true,
			providers:    []AuthProvider{&mockNonRenewableProvider{creds: &PassKeyChain{Username: "user", Password: "pass"}}},
			wantUser:     "user",
			wantStored:   false,
		},
		{
			name:      "works when store is nil",
			providers: []AuthProvider{&mockProvider{creds: &PassKeyChain{Username: "user", Password: "pass"}}},
			wantUser:  "user",
		},
	}

	const ref = "docker.io/library/nginx:latest"

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldStore := renewalStore
			defer func() { renewalStore = oldStore }()

			if tt.storeEnabled {
				renewalStore = newCredentialStore(5 * time.Minute)
				if tt.cached != nil {
					renewalStore.Add(ref, tt.cached)
				}
			} else {
				renewalStore = nil
			}

			got := getRegistryKeyChainFromProviders(ref, nil, tt.providers)
			require.NotNil(t, got)
			assert.Equal(t, tt.wantUser, got.Username)

			if tt.storeEnabled {
				stored := renewalStore.Get(ref)
				if tt.wantStored {
					assert.NotNil(t, stored)
				} else {
					assert.Nil(t, stored)
				}
			}
		})
	}
}

// --- InitCredentialRenewal ---

// TestInitCredentialRenewalLifecycle verifies that the renewal goroutine
// starts, ticks, and stops cleanly on context cancellation. The RAFS cache
// is empty in the test environment so reconcile is a no-op each tick.
func TestInitCredentialRenewalLifecycle(t *testing.T) {
	oldStore := renewalStore
	defer func() { renewalStore = oldStore }()

	ctx, cancel := context.WithCancel(t.Context())

	InitCredentialRenewal(ctx, 30*time.Millisecond)
	require.NotNil(t, renewalStore)

	// Let it tick a few times without error.
	time.Sleep(100 * time.Millisecond)

	cancel()
	// Give goroutine time to observe cancellation.
	time.Sleep(60 * time.Millisecond)
}
