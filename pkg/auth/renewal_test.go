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
	creds     *PassKeyChain
	err       error
	callCount int
}

func (m *mockProvider) GetCredentials(_ *AuthRequest) (*PassKeyChain, error) {
	m.callCount++
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
		return nil, nil
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
				s.Add("ref", &mockProvider{}, &PassKeyChain{Username: "user", Password: "pass"})
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
				s.Add("ref", &mockProvider{}, &PassKeyChain{Username: "user", Password: "pass"})
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
				s.Add("ref", &mockProvider{}, &PassKeyChain{Username: "user", Password: "pass"})
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

func TestCredentialStoreOverwrite(t *testing.T) {
	store := newCredentialStore(5 * time.Minute)
	provider := &mockProvider{}

	store.Add("ref", provider, &PassKeyChain{Username: "old", Password: "old"})
	store.Add("ref", provider, &PassKeyChain{Username: "new", Password: "new"})

	got := store.Get("ref")
	require.NotNil(t, got)
	assert.Equal(t, "new", got.Username)
}

func TestCredentialStoreEntries(t *testing.T) {
	store := newCredentialStore(5 * time.Minute)
	provider := &mockProvider{}

	store.Add("ref1", provider, &PassKeyChain{Username: "u1", Password: "p1"})
	store.Add("ref2", provider, &PassKeyChain{Username: "u2", Password: "p2"})

	entries := store.Entries()
	assert.Len(t, entries, 2)
}

func TestCredentialStoreUpdateKeychain(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(s *credentialStore)
		ref      string
		wantNil  bool
		wantUser string
	}{
		{
			name: "updates existing entry",
			setup: func(s *credentialStore) {
				s.Add("ref", &mockProvider{}, &PassKeyChain{Username: "old", Password: "old"})
			},
			ref:      "ref",
			wantUser: "updated",
		},
		{
			name:    "no-op for nonexistent ref",
			setup:   func(_ *credentialStore) {},
			ref:     "missing",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newCredentialStore(5 * time.Minute)
			tt.setup(store)

			store.updateKeychain(tt.ref, &PassKeyChain{Username: "updated", Password: "updated"})

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

func TestCredentialStoreConcurrency(t *testing.T) {
	store := newCredentialStore(5 * time.Minute)
	provider := &mockProvider{}

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ref := "ref"
			kc := &PassKeyChain{Username: "user", Password: "pass"}
			store.Add(ref, provider, kc)
			store.Get(ref)
			store.Entries()
			if i%2 == 0 {
				store.Remove(ref)
			}
		}(i)
	}
	wg.Wait()
}

// --- RemoveCredentials ---

func TestRemoveCredentials(t *testing.T) {
	tests := []struct {
		name  string
		setup func()
		ref   string
	}{
		{
			name: "removes from active store",
			setup: func() {
				renewalStore = newCredentialStore(5 * time.Minute)
				renewalStore.Add("ref", &mockProvider{}, &PassKeyChain{Username: "u", Password: "p"})
			},
			ref: "ref",
		},
		{
			name:  "no-op when store is nil",
			setup: func() { renewalStore = nil },
			ref:   "ref",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldStore := renewalStore
			defer func() { renewalStore = oldStore }()

			tt.setup()
			RemoveCredentials(tt.ref)

			if renewalStore != nil {
				assert.Nil(t, renewalStore.Get(tt.ref))
			}
		})
	}
}

// --- renewCredentials ---

func TestRenewCredentials(t *testing.T) {
	tests := []struct {
		name     string
		provider *trackingProvider
		wantUser string
		wantCall int32
	}{
		{
			name:     "updates keychain on success",
			provider: &trackingProvider{},
			wantUser: "user-v1",
			wantCall: 1,
		},
		{
			name: "keeps old keychain on failure",
			provider: func() *trackingProvider {
				p := &trackingProvider{}
				p.failNext.Store(true)
				return p
			}(),
			wantUser: "original",
			wantCall: 1,
		},
		{
			name: "keeps old keychain when provider returns nil",
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
			store := newCredentialStore(5 * time.Minute)
			store.Add("ref1", tt.provider, &PassKeyChain{Username: "original", Password: "original"})

			renewCredentials(store)

			assert.Equal(t, tt.wantCall, tt.provider.calls.Load())
			got := store.Get("ref1")
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
					renewalStore.Add(ref, &mockProvider{}, tt.cached)
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

func TestInitCredentialRenewalContextCancel(t *testing.T) {
	oldStore := renewalStore
	defer func() { renewalStore = oldStore }()

	ctx, cancel := context.WithCancel(context.Background())

	InitCredentialRenewal(ctx, 50*time.Millisecond, nil)
	require.NotNil(t, renewalStore)

	// Let it tick at least once
	time.Sleep(100 * time.Millisecond)

	cancel()
	// Give goroutine time to stop
	time.Sleep(100 * time.Millisecond)
}

func TestInitCredentialRenewalSeeding(t *testing.T) {
	oldStore := renewalStore
	defer func() { renewalStore = oldStore }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	InitCredentialRenewal(ctx, 5*time.Minute, []string{"docker.io/library/nginx:latest"})
	require.NotNil(t, renewalStore)

	// Seeding calls GetRegistryKeyChain which queries real providers.
	// In a test environment, no provider will return creds, so the store
	// should remain empty (no renewable provider matched). This test
	// verifies the seeding code path doesn't panic.
}

// --- Lifecycle test ---

// TestCredentialRenewalLifecycle exercises the full renewal lifecycle via
// InitCredentialRenewal: seed -> renewal loop updates creds -> serve from
// store -> evict via RemoveCredentials.
func TestCredentialRenewalLifecycle(t *testing.T) {
	oldStore := renewalStore
	oldBuildProviders := buildProviders
	defer func() {
		renewalStore = oldStore
		buildProviders = oldBuildProviders
	}()

	const (
		ref1 = "docker.io/library/nginx:latest"
		ref2 = "docker.io/library/redis:latest"
	)
	interval := 50 * time.Millisecond
	provider := &trackingProvider{}

	// Inject a mock provider so InitCredentialRenewal seeds from it.
	buildProviders = func() []AuthProvider {
		return []AuthProvider{provider}
	}

	// 1. Init seeds the store by calling GetRegistryKeyChain for each existing ref.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	InitCredentialRenewal(ctx, interval, []string{ref1, ref2})

	require.NotNil(t, renewalStore)
	assert.Equal(t, int32(2), provider.calls.Load(), "seeding should call provider once per ref")
	assert.Len(t, renewalStore.Entries(), 2)

	kc1 := renewalStore.Get(ref1)
	require.NotNil(t, kc1)
	assert.Equal(t, "user-v1", kc1.Username)

	kc2 := renewalStore.Get(ref2)
	require.NotNil(t, kc2)
	assert.Equal(t, "user-v2", kc2.Username)

	// 2. Verify GetRegistryKeyChain serves from the store without hitting the provider.
	cached := GetRegistryKeyChain(ref1, nil)
	require.NotNil(t, cached)
	assert.Equal(t, "user-v1", cached.Username, "should serve from cache")
	assert.Equal(t, int32(2), provider.calls.Load(), "cache hit should not call provider")

	// 3. Wait for the renewal loop to tick at least once.
	time.Sleep(3 * interval)

	got1 := renewalStore.Get(ref1)
	require.NotNil(t, got1)
	assert.NotEqual(t, "user-v1", got1.Username, "ref1 credentials should be renewed")

	got2 := renewalStore.Get(ref2)
	require.NotNil(t, got2)
	assert.NotEqual(t, "user-v2", got2.Username, "ref2 credentials should be renewed")

	callsAfterRenewal := provider.calls.Load()
	assert.Greater(t, callsAfterRenewal, int32(2), "renewal loop should have called the provider")

	// 4. Evict ref1 and verify it's gone while ref2 remains.
	RemoveCredentials(ref1)
	assert.Nil(t, renewalStore.Get(ref1), "ref1 should be evicted")
	assert.NotNil(t, renewalStore.Get(ref2), "ref2 should still be cached")

	// 5. Fetching evicted ref1 falls through to the provider again.
	fresh := GetRegistryKeyChain(ref1, nil)
	require.NotNil(t, fresh)
	assert.Greater(t, provider.calls.Load(), callsAfterRenewal, "evicted ref should trigger a new provider call")

	// 6. Cancel context and verify the loop stops cleanly.
	cancel()
	time.Sleep(2 * interval)
}
