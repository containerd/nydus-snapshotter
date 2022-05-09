/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package nydus

import (
	"os"
	"testing"
)

func Test_writeBootsrapFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp(os.TempDir(), "test")
	if err != nil {
		t.Fatalf("create temp test dir failed %s", err)
	}
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name      string
		bootstarp string
		fileSize  uint64
		hasError  bool
	}{
		{
			"test_v6_remove_chunk",
			"testdata/v6-bootstrap-chunk-pos-438272.tar.gz",
			438272,
			false,
		},
		{
			"do_nothing_for_v5",
			"testdata/v5-bootstrap-file-size-736032.tar.gz",
			736032,
			false,
		},
		// no image.boot file
		{
			"invalid_bootstrap",
			"testdata/invalid.tar.gz",
			0,
			true,
		},
		// There is an image.boot file, but the content of the file is invalid. At this time,
		// the file will be downloaded and decompressed normally. Leave it to nydusd to handle it.
		{
			"invalid_bootstrap",
			"testdata/invalid-bootstrap-file-size-133513.tar.gz",
			133513,
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := os.Open(tt.bootstarp)
			if err != nil {
				t.Fatalf("open test file %s failed %s", tt.bootstarp, err)
			}
			rawBootstrapFile, err := os.CreateTemp(tmpDir, "bootstrap")
			if err != nil {
				t.Fatalf("failed to create bootstrap file %s", err)
			}

			err = writeBootstrapToFile(file, rawBootstrapFile)
			if tt.hasError && err == nil {
				t.Errorf("writeBootstrapToFile expect return error, but actual is nil")
			}

			if !tt.hasError && err != nil {
				t.Errorf("writeBootstrapToFile expect return nil, but actual is %s", err)
			}

			info, err := rawBootstrapFile.Stat()
			if err != nil {
				t.Fatalf("failed to get bootstrap file info %s", err)
			}

			if info.Size() != int64(tt.fileSize) {
				t.Errorf("expect generated bootstrap file size is %d, but actual is %d", tt.fileSize, info.Size())
			}
		})
	}
}
