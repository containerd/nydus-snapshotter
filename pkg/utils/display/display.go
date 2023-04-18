/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package display

import "fmt"

func ByteToReadableIEC(b uint32) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB",
		float64(b)/float64(div), "KMGTPE"[exp])
}

func MicroSecondToReadable(b uint64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d us", b)
	}

	if b < unit*unit {
		return fmt.Sprintf("%.3f ms", float64(b)/float64(unit))
	}

	return fmt.Sprintf("%.3f s", float64(b)/float64(unit*unit))
}
