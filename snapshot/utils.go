/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
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
