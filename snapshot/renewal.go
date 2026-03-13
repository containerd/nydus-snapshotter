/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"context"
	"time"

	"github.com/containerd/log"

	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	mgr "github.com/containerd/nydus-snapshotter/pkg/manager"
)

// startCredentialRenewal initializes the credential store, runs an initial
// reconciliation, and starts a background goroutine that periodically
// renews credentials and hot-reloads running nydusd daemons.
func startCredentialRenewal(ctx context.Context, interval time.Duration, managers []*mgr.Manager) {
	auth.InitCredentialStore(interval)
	reconcileCredentials(ctx, managers)

	log.G(ctx).WithField("interval", interval).Info("credential renewal initialized")
	go credentialRenewalLoop(ctx, interval, managers)
}

// credentialRenewalLoop is the background goroutine that periodically
// reconciles and renews credentials.
func credentialRenewalLoop(ctx context.Context, interval time.Duration, managers []*mgr.Manager) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcileCredentials(ctx, managers)
		}
	}
}

// reconcileCredentials reconciles the credential store against the live RAFS
// instances by walking managers -> daemons -> rafs, and renews all entries
// that should be active. Stale store entries (refs no longer backed by a
// running rafs) are evicted.
// 4 different situations are possible for each live ref:
// - entry in store, live in RAFS: renew + hot-reload (fusedev only)
// - entry not in store, live in RAFS: add + hot-reload (fusedev only)
// - entry not in store, not live in RAFS: nothing
// - entry in store, not live in RAFS: evict
func reconcileCredentials(ctx context.Context, managers []*mgr.Manager) {
	live := make(map[string]struct{})

	for _, m := range managers {
		for _, d := range m.ListDaemons() {
			if d.State() != types.DaemonStateRunning {
				continue
			}

			for _, r := range d.RafsCache.List() {
				if r.ImageID == "" {
					continue
				}

				live[r.ImageID] = struct{}{}

				log.L.WithField("ref", r.ImageID).Debug("renewing credential entry")
				kc := auth.RenewCredential(r.ImageID)
				if kc == nil {
					continue
				}

				if err := d.UpdateAuthConfig(r.SnapshotID, kc); err != nil {
					log.G(ctx).WithError(err).WithField("daemon", d.ID()).
						Warn("failed to hot-reload auth config")
				}
			}
		}
	}

	auth.EvictStaleCredentials(live)
}
