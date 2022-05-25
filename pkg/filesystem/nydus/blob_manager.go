package nydus

import (
	"context"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/log"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

type BlobManager struct {
	blobDir   string
	eventChan chan string
}

func NewBlobManager(blobDir string) *BlobManager {
	return &BlobManager{
		blobDir: blobDir,
		// TODO(tianqian.zyf): Remove hardcode chan buffer
		eventChan: make(chan string, 8),
	}
}

func (b *BlobManager) Run(ctx context.Context) error {
	log.G(ctx).Info("blob manager goroutine start...")
	for {
		select {
		case id := <-b.eventChan:
			err := b.cleanupBlob(id)
			if err != nil {
				log.G(ctx).Warnf("delete blob %s failed", id)
			} else {
				log.G(ctx).Infof("delete blob %s success", id)
			}
		case <-ctx.Done():
			log.G(ctx).Infof("exit from BlobManger")
			return ctx.Err()
		}
	}
}

func (b *BlobManager) GetBlobDir() string {
	return b.blobDir
}

func (b *BlobManager) cleanupBlob(id string) error {
	id, err := b.decodeId(id)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(b.blobDir, id))
}

func (b *BlobManager) decodeId(id string) (string, error) {
	digest, err := digest.Parse(id)
	if err != nil {
		return "", errors.Wrapf(err, "invalid blob layer digest %s", id)
	}
	return digest.Encoded(), nil
}

func (b *BlobManager) Remove(id string, async bool) error {
	if async {
		b.eventChan <- id
		return nil
	}
	return b.cleanupBlob(id)
}
