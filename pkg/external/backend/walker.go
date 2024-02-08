package backend

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
)

type Walker struct {
}

func NewWalker() *Walker {
	return &Walker{}
}

func bfsWalk(path string, fn func(string, fs.FileInfo) error) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if info.IsDir() {
		files, err := os.ReadDir(path)
		if err != nil {
			return err
		}

		dirs := []string{}
		for _, file := range files {
			filePath := filepath.Join(path, file.Name())
			if file.Type().IsRegular() {
				info, err := file.Info()
				if err != nil {
					return err
				}
				if err := fn(filePath, info); err != nil {
					return err
				}
			}
			if file.IsDir() {
				dirs = append(dirs, filePath)
			}
		}

		for _, dir := range dirs {
			if err := bfsWalk(dir, fn); err != nil {
				return err
			}
		}
	}

	return nil
}

func (walker *Walker) Walk(ctx context.Context, root string, handler Handler) (*Result, error) {
	chunks := []Chunk{}
	files := []string{}

	addFile := func(size int64, relativeTarget string) error {
		target := filepath.Join("/", relativeTarget)
		_chunks, err := handler.Handle(ctx, File{
			RelativePath: relativeTarget,
			Size:         size,
		})
		if err != nil {
			return err
		}
		chunks = append(chunks, _chunks...)
		files = append(files, target)
		return nil
	}

	walkFiles := []func() error{}

	if err := bfsWalk(root, func(path string, info fs.FileInfo) error {
		if info.Size() < DefaultThrottleFileSize {
			return nil
		}

		target, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		walkFiles = append(walkFiles, func() error {
			return addFile(info.Size(), target)
		})

		return nil
	}); err != nil {
		return nil, errors.Wrap(err, "walk directory")
	}

	for i := 0; i < len(walkFiles); i++ {
		if err := walkFiles[i](); err != nil {
			return nil, errors.Wrap(err, "handle files")
		}
	}

	bkd, err := handler.Backend(ctx)
	if err != nil {
		return nil, err
	}

	return &Result{
		Chunks:  chunks,
		Files:   files,
		Backend: *bkd,
	}, nil
}
