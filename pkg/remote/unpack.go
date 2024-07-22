/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package remote

import (
	"archive/tar"
	"fmt"
	"io"
	"os"

	"github.com/containerd/containerd/v2/pkg/archive/compression"
)

// Unpack unpacks the file named `source` in tar stream
// and write into `target` path.
func Unpack(reader io.Reader, source, target string) error {
	rdr, err := compression.DecompressStream(reader)
	if err != nil {
		return err
	}
	defer rdr.Close()

	found := false
	tr := tar.NewReader(rdr)
	for {
		hdr, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		if hdr.Name == source {
			file, err := os.Create(target)
			if err != nil {
				return err
			}
			defer file.Close()
			if _, err := io.Copy(file, tr); err != nil {
				return err
			}
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("not found file %s in tar", source)
	}

	return nil
}
