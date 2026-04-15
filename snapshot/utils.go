/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"fmt"

	"github.com/containerd/continuity/fs"
	"github.com/pkg/errors"
)

func getSupportsDType(dir string) (bool, error) {
	return fs.SupportsDType(dir)
}

// parseIDMappingHostID parses an ID mapping string "containerID:hostID:size"
// (e.g. "0:1000:65536") and returns the hostID. Only containerID=0 is supported.
func parseIDMappingHostID(mapping string) (int, error) {
	var (
		ctrID  int
		hostID int
		length int
	)
	if _, err := fmt.Sscanf(mapping, "%d:%d:%d", &ctrID, &hostID, &length); err != nil {
		return -1, errors.Wrapf(err, "failed to parse ID mapping %q", mapping)
	}
	if ctrID < 0 || hostID < 0 || length <= 0 {
		return -1, errors.Errorf("invalid mapping %q: IDs must be non-negative and size must be positive", mapping)
	}
	if ctrID != 0 {
		return -1, errors.Errorf("only container ID 0 is supported in ID mapping, got %d", ctrID)
	}
	return hostID, nil
}
