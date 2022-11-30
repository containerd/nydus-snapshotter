/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package exporter

type Exporter interface {
	// Export all metrics data.
	Export()
}

func FileExport() error {
	return GlobalFileExporter.Export()
}
