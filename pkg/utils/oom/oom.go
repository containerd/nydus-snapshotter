/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package oom

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

var (
	OOMScoreAdjMin = -1000
	OOMScoreAdjMax = 1000
)

func ReadOOMScoreAdj(path string) (int, error) {
	oomBuf, err := os.ReadFile(path)
	if err != nil {
		return 0, errors.Wrapf(err, "read file %s", path)
	}

	oom, err := strconv.Atoi(strings.ReplaceAll(string(oomBuf), "\n", ""))
	if err != nil {
		return 0, errors.Wrapf(err, "convert %s to integer", string(oomBuf))
	}
	return oom, nil
}

func WriteOOMScoreAdj(path string, oomScoreAdj int) error {
	return os.WriteFile(path, []byte(fmt.Sprint(oomScoreAdj)), 0644)
}

// Change the oom_score_adj of target process if the oom_score_adj of
// current process is equal to OOM_SCORE_ADJ_MIN.
// If the target's oom_score_adj is already greater than OOM_SCORE_ADJ_MIN, skip it.
func ChangeDaemonOOMScoreAdj(pid, scodeAdj int) error {
	currentOOMPath := "/proc/self/oom_score_adj"
	currentOOM, err := ReadOOMScoreAdj(currentOOMPath)
	if err != nil {
		return errors.Wrapf(err, "read oom_score_adj file %s", currentOOMPath)
	}
	if currentOOM > OOMScoreAdjMin {
		return nil
	}

	daemonOOMPath := fmt.Sprintf("/proc/%d/oom_score_adj", pid)
	daemonOOM, err := ReadOOMScoreAdj(currentOOMPath)
	if err != nil {
		return errors.Wrapf(err, "read oom_score_adj file %s", daemonOOMPath)
	}
	if daemonOOM > OOMScoreAdjMin {
		return nil
	}

	return WriteOOMScoreAdj(daemonOOMPath, scodeAdj)
}
