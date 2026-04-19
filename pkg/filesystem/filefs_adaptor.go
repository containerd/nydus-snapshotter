/*
 * Copyright (c) 2024. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package filesystem

// FilefsEnabled returns true if the file-backed EROFS driver is enabled.
func (fs *Filesystem) FilefsEnabled() bool {
	return fs.filefsMgr != nil
}
