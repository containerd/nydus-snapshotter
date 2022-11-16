/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */
package blob

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/nydus-snapshotter/pkg/resolve"
	"github.com/stretchr/testify/assert"
)

func Test_Remove(t *testing.T) {
	dir, err := os.MkdirTemp(os.TempDir(), "blb")
	if err != nil {
		t.Fatalf("failed to make dir %s", dir)
	}
	defer os.RemoveAll(dir)
	id := "038f2b2815ae3c309b77bf34bf6ce988c922b7718773b4c98d5cd2b76c35a146"
	file, err := os.Create(filepath.Join(dir, id))
	if err != nil {
		t.Fatalf("failed to create dir %s", dir)
	}
	file.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	blobMgr := NewBlobManager(dir, resolve.NewResolver())
	go func() {
		err := blobMgr.Run(ctx)
		assert.Nil(t, err)
	}()
	err = blobMgr.Remove(id, true)
	assert.Nil(t, err)
	// wait to deleted the file
	time.Sleep(time.Millisecond * 100)
	_, err = os.Stat(filepath.Join(dir, "sha256:"+id))
	if err == nil || !os.IsNotExist(err) {
		t.Fatalf("expect the blob file not exits, but actual exists")
	}
}

func Test_decodeID(t *testing.T) {
	blobMgr := NewBlobManager("dir", resolve.NewResolver())
	tests := []struct {
		name     string
		id       string
		decodeID string
		hasError bool
	}{
		{
			name:     "ok",
			id:       "sha256:038f2b2815ae3c309b77bf34bf6ce988c922b7718773b4c98d5cd2b76c35a146",
			decodeID: "038f2b2815ae3c309b77bf34bf6ce988c922b7718773b4c98d5cd2b76c35a146",
			hasError: false,
		},
		{
			name:     "not ok",
			id:       "038f2b2815ae3c309b77bf34bf6ce988c922b7718773b4c98d5cd2b76c35a146",
			decodeID: "",
			hasError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			realID, err := blobMgr.decodeID(tt.id)
			if tt.hasError {
				if err == nil {
					t.Fatal("expect has error, but actual is nil")
				}
			} else {
				if err != nil {
					t.Fatalf("expect doesn't have error, but actual is %s", err)
				}
				if realID != tt.decodeID {
					t.Fatalf("expect doesn't have error, but actual is %s", err)
				}
			}
		})
	}
}
