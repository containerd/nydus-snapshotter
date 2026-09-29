/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"github.com/containerd/log"
	sd "github.com/coreos/go-systemd/v22/daemon"
)

// notifySystemd reports a lifecycle state to the service manager (sd_notify).
// It is a no-op unless the snapshotter runs under systemd with Type=notify,
// i.e. NOTIFY_SOCKET is set in the environment.
func notifySystemd(state string) {
	sent, err := sd.SdNotify(false, state)
	if err != nil {
		log.L.WithError(err).Warnf("failed to notify service manager: %s", state)
		return
	}
	if sent {
		log.L.Infof("notified service manager: %s", state)
	}
}

// notifyReady tells the service manager that the snapshotter has finished
// recovery and its gRPC socket is ready to accept connections. With
// Type=notify, systemd keeps the unit "activating" until this point, allowing
// dependents to wait for actual readiness instead of merely process startup.
func notifyReady() {
	notifySystemd(sd.SdNotifyReady)
}

// notifyStopping tells the service manager the snapshotter began shutting
// down.
func notifyStopping() {
	notifySystemd(sd.SdNotifyStopping)
}
