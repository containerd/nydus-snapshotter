/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"os"
	"syscall"

	"github.com/containerd/continuity/fs"
	"github.com/containerd/nydus-snapshotter/pkg/label"
)

func isNydusDataLayer(labels map[string]string) bool {
	_, ok := labels[label.NydusDataLayer]
	return ok
}

func isNydusMetaLayer(labels map[string]string) bool {
	_, ok := labels[label.NydusMetaLayer]
	return ok
}

func getSupportsDType(dir string) (bool, error) {
	return fs.SupportsDType(dir)
}

func lchown(target string, st os.FileInfo) error {
	stat := st.Sys().(*syscall.Stat_t)
	return os.Lchown(target, int(stat.Uid), int(stat.Gid))
}
