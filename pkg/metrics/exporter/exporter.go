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

func NewFileExporter(opts ...Opt) error {
	for _, o := range opts {
		if err := o(GlobalFileExporter); err != nil {
			return err
		}
	}

	return nil
}

func ExportToFile() error {
	return GlobalFileExporter.Export()
}
