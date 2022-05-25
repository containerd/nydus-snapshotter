package nydus

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	blobMgr := NewBlobManager(dir)
	go func() {
		blobMgr.Run(ctx)
	}()
	blobMgr.Remove(id, true)
	// wait to deleted the file
	time.Sleep(time.Millisecond * 100)
	_, err = os.Stat(filepath.Join(dir, "sha256:"+id))
	if err == nil || !os.IsNotExist(err) {
		t.Fatalf("expect the blob file not exits, but actual exists")
	}
}

func Test_decodeId(t *testing.T) {
	blobMgr := NewBlobManager("dir")
	tests := []struct {
		name     string
		id       string
		decodeId string
		hasError bool
	}{
		{
			name:     "ok",
			id:       "sha256:038f2b2815ae3c309b77bf34bf6ce988c922b7718773b4c98d5cd2b76c35a146",
			decodeId: "038f2b2815ae3c309b77bf34bf6ce988c922b7718773b4c98d5cd2b76c35a146",
			hasError: false,
		},
		{
			name:     "not ok",
			id:       "038f2b2815ae3c309b77bf34bf6ce988c922b7718773b4c98d5cd2b76c35a146",
			decodeId: "",
			hasError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			realId, err := blobMgr.decodeId(tt.id)
			if tt.hasError {
				if err == nil {
					t.Fatal("expect has error, but actual is nil")
				}
			} else {
				if err != nil {
					t.Fatalf("expect doesn't have error, but actual is %s", err)
				}
				if realId != tt.decodeId {
					t.Fatalf("expect doesn't have error, but actual is %s", err)
				}
			}
		})
	}
}
