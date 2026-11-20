/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package mount

import "golang.org/x/sys/unix"

const (
	// umountForce aborts in-flight requests (needed for disconnected FUSE mounts).
	umountForce = unix.MNT_FORCE
	// umountDetach performs a lazy unmount, detaching a busy mountpoint from the
	// namespace and cleaning it up once it is no longer referenced.
	umountDetach = unix.MNT_DETACH
)
