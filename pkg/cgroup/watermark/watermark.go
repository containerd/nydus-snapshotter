/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package watermark

import (
	"os"
	"strconv"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/utils/file"
)

func UpdateScaleFactor(path string, memoryWatermarkScaleFactor int64) error {
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			log.L.Infof("the memory watermark scale factor is not supported")
			return nil
		}
		return err
	}
	return file.WriteFile(path, []byte(strconv.FormatInt(memoryWatermarkScaleFactor, 10)))
}
