/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package mount

import "golang.org/x/sys/unix"

// darwin has no lazy (MNT_DETACH) unmount; degrade the last resort to a forced
// unmount. This build exists mainly for local development on macOS; nydusd
// mounts are only managed at runtime on Linux.
const (
	umountForce  = unix.MNT_FORCE
	umountDetach = unix.MNT_FORCE
)
