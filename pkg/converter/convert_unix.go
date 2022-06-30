//go:build !windows
// +build !windows

/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package converter

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"

	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/content"
	"github.com/containerd/fifo"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"

	"github.com/containerd/nydus-snapshotter/pkg/converter/tool"
)

const bootstrapNameInTar = "image.boot"
const blobNameInTar = "image.blob"

const envNydusBuilder = "NYDUS_BUILDER"
const envNydusWorkdir = "NYDUS_WORKDIR"

func getBuilder() string {
	builderPath := os.Getenv(envNydusBuilder)
	if builderPath == "" {
		builderPath = "nydus-image"
	}
	return builderPath
}

// Unpack a OCI formatted tar stream into a directory.
func unpackOciTar(ctx context.Context, dst string, reader io.Reader) error {
	ds, err := compression.DecompressStream(reader)
	if err != nil {
		return errors.Wrap(err, "unpack stream")
	}
	defer ds.Close()

	if _, err := archive.Apply(
		ctx,
		dst,
		ds,
		archive.WithConvertWhiteout(func(hdr *tar.Header, file string) (bool, error) {
			// Keep to extract all whiteout files.
			return true, nil
		}),
	); err != nil {
		return errors.Wrap(err, "apply with convert whiteout")
	}

	return nil
}

// Unpack a Nydus formatted tar stream into a directory.
func unpackNydusTar(ctx context.Context, bootDst, blobDst string, ra content.ReaderAt) error {
	boot, err := os.OpenFile(bootDst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return errors.Wrapf(err, "write to bootstrap %s", bootDst)
	}
	defer boot.Close()

	if err = unpackBootstrapFromNydusTar(ctx, ra, boot); err != nil {
		return errors.Wrap(err, "unpack bootstrap from nydus")
	}

	blob, err := os.OpenFile(blobDst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return errors.Wrapf(err, "write to blob %s", blobDst)
	}
	defer blob.Close()

	if err = unpackBlobFromNydusTar(ctx, ra, blob); err != nil {
		return errors.Wrap(err, "unpack blob from nydus")
	}

	return nil
}

// Unpack the bootstrap from nydus formatted tar stream (blob + bootstrap).
// The nydus formatted tar stream is a tar-like structure that arranges the
// data as follows:
//
// `blob_data | blob_tar_header | bootstrap_data | boostrap_tar_header`
func unpackBootstrapFromNydusTar(ctx context.Context, ra content.ReaderAt, target io.Writer) error {
	cur := ra.Size()
	reader := newSeekReader(ra)

	const headerSize = 512

	// Seek from tail to head of nydus formatted tar stream to find nydus
	// bootstrap data.
	for {
		if headerSize > cur {
			return fmt.Errorf("invalid tar format at pos %d", cur)
		}

		// Try to seek to the part of tar header.
		var err error
		cur, err = reader.Seek(cur-headerSize, io.SeekCurrent)
		if err != nil {
			return errors.Wrapf(err, "seek to %d for tar header", cur-headerSize)
		}

		tr := tar.NewReader(reader)
		// Parse tar header.
		hdr, err := tr.Next()
		if err != nil {
			return errors.Wrap(err, "parse tar header")
		}

		if hdr.Name == bootstrapNameInTar {
			// Try to seek to the part of tar data (bootstrap_data).
			if hdr.Size > cur {
				return fmt.Errorf("invalid tar format at pos %d", cur)
			}
			bootstrapOffset := cur - hdr.Size
			_, err = reader.Seek(bootstrapOffset, io.SeekStart)
			if err != nil {
				return errors.Wrap(err, "seek to bootstrap data offset")
			}

			// Copy tar data (bootstrap_data) to provided target writer.
			if _, err := io.CopyN(target, reader, hdr.Size); err != nil {
				return errors.Wrap(err, "copy bootstrap data to reader")
			}

			return nil
		}

		if cur == hdr.Size {
			break
		}
	}

	return fmt.Errorf("can't find bootstrap in nydus tar")
}

func unpackBlobFromNydusTar(ctx context.Context, ra content.ReaderAt, target io.Writer) error {
	cur := ra.Size()
	reader := newSeekReader(ra)

	const headerSize = 512

	// Seek from tail to head of nydus formatted tar stream to find nydus
	// bootstrap data.
	for {
		if headerSize > cur {
			break
		}

		// Try to seek to the part of tar header.
		var err error
		cur, err = reader.Seek(cur-headerSize, io.SeekStart)
		if err != nil {
			return errors.Wrapf(err, "seek to %d for tar header", cur-headerSize)
		}

		tr := tar.NewReader(reader)
		// Parse tar header.
		hdr, err := tr.Next()
		if err != nil {
			return errors.Wrap(err, "parse tar header")
		}

		if hdr.Name == bootstrapNameInTar {
			if hdr.Size > cur {
				return fmt.Errorf("invalid tar format at pos %d", cur)
			}
			cur, err = reader.Seek(cur-hdr.Size, io.SeekStart)
			if err != nil {
				return errors.Wrap(err, "seek to bootstrap data offset")
			}
		} else if hdr.Name == blobNameInTar {
			if hdr.Size > cur {
				return fmt.Errorf("invalid tar format at pos %d", cur)
			}
			_, err = reader.Seek(cur-hdr.Size, io.SeekStart)
			if err != nil {
				return errors.Wrap(err, "seek to blob data offset")
			}
			if _, err := io.CopyN(target, reader, hdr.Size); err != nil {
				return errors.Wrap(err, "copy blob data to reader")
			}
			return nil
		}
	}

	return nil
}

// Pack converts a OCI formatted tar stream to a nydus formatted tar stream
//
// The nydus blob tar stream contains blob and bootstrap files with the following
// file tree structure:
//
// /image
// ├── image.blob
// ├── image.boot
//
// So for the chunk of files in the nydus boostreap, a blob compressed offset
// of 1024 (size_of(tar_header) * 2) is required.
//
// Important: the caller must check `io.WriteCloser.Close() == nil` to ensure
// the conversion workflow is finish.
func Pack(ctx context.Context, dest io.Writer, opt PackOption) (io.WriteCloser, error) {
	workDir, err := ioutil.TempDir(os.Getenv(envNydusWorkdir), "nydus-converter-")
	if err != nil {
		return nil, errors.Wrap(err, "create work directory")
	}
	defer func() {
		if err != nil {
			os.RemoveAll(workDir)
		}
	}()

	sourceDir := filepath.Join(workDir, "source")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		return nil, errors.Wrap(err, "create source directory")
	}

	pr, pw := io.Pipe()

	unpackDone := make(chan bool, 1)
	go func() {
		if err := unpackOciTar(ctx, sourceDir, pr); err != nil {
			pr.CloseWithError(errors.Wrapf(err, "unpack to %s", sourceDir))
			close(unpackDone)
			return
		}
		unpackDone <- true
	}()

	wc := newWriteCloser(pw, func() error {
		defer func() {
			os.RemoveAll(workDir)
		}()

		// Because PipeWriter#Close is called does not mean that the PipeReader
		// has finished reading all the data, and unpack may not be complete yet,
		// so we need to wait for that here.
		<-unpackDone

		blobPath := filepath.Join(workDir, "blob")
		blobFifo, err := fifo.OpenFifo(ctx, blobPath, syscall.O_CREAT|syscall.O_RDONLY|syscall.O_NONBLOCK, 0644)
		if err != nil {
			return errors.Wrapf(err, "create fifo file")
		}
		defer blobFifo.Close()

		go func() {
			err := tool.Pack(tool.PackOption{
				BuilderPath: getBuilder(),

				BlobPath:         blobPath,
				RafsVersion:      opt.RafsVersion,
				SourcePath:       sourceDir,
				ChunkDictPath:    opt.ChunkDictPath,
				PrefetchPatterns: opt.PrefetchPatterns,
			})
			if err != nil {
				pw.CloseWithError(errors.Wrapf(err, "convert blob for %s", sourceDir))
				blobFifo.Close()
			}
		}()

		if _, err := io.Copy(dest, blobFifo); err != nil {
			return errors.Wrap(err, "pack nydus tar")
		}

		return nil
	})

	return wc, nil
}

// Merge multiple nydus boostraps (from every layer of image) to a final boostrap.
func Merge(ctx context.Context, layers []Layer, dest io.Writer, opt MergeOption) error {
	workDir, err := ioutil.TempDir(os.Getenv(envNydusWorkdir), "nydus-converter-")
	if err != nil {
		return errors.Wrap(err, "create work directory")
	}
	defer os.RemoveAll(workDir)

	eg, ctx := errgroup.WithContext(ctx)
	sourceBootstrapPaths := []string{}
	for idx := range layers {
		sourceBootstrapPaths = append(sourceBootstrapPaths, filepath.Join(workDir, layers[idx].Digest.Hex()))
		eg.Go(func(idx int) func() error {
			return func() error {
				layer := layers[idx]

				// Use the hex hash string of whole tar blob as the boostrap name.
				bootstrap, err := os.Create(filepath.Join(workDir, layer.Digest.Hex()))
				if err != nil {
					return errors.Wrap(err, "create source bootstrap")
				}
				defer bootstrap.Close()

				if err := unpackBootstrapFromNydusTar(ctx, layer.ReaderAt, bootstrap); err != nil {
					return errors.Wrap(err, "unpack nydus tar")
				}

				return nil
			}
		}(idx))
	}

	if err := eg.Wait(); err != nil {
		return errors.Wrap(err, "unpack all bootstraps")
	}

	targetBootstrapPath := filepath.Join(workDir, "bootstrap")

	if err := tool.Merge(tool.MergeOption{
		BuilderPath: getBuilder(),

		SourceBootstrapPaths: sourceBootstrapPaths,
		TargetBootstrapPath:  targetBootstrapPath,
		ChunkDictPath:        opt.ChunkDictPath,
		PrefetchPatterns:     opt.PrefetchPatterns,
	}); err != nil {
		return errors.Wrap(err, "merge bootstrap")
	}

	var rc io.ReadCloser

	if opt.WithTar {
		rc, err = packToTar(targetBootstrapPath, fmt.Sprintf("image/%s", bootstrapNameInTar), false)
		if err != nil {
			return errors.Wrap(err, "pack bootstrap to tar")
		}
	} else {
		rc, err = os.Open(targetBootstrapPath)
		if err != nil {
			return errors.Wrap(err, "open targe bootstrap")
		}
	}
	defer rc.Close()

	if _, err = io.Copy(dest, rc); err != nil {
		return errors.Wrap(err, "copy merged bootstrap")
	}

	return nil
}

func Unpack(ctx context.Context, ia content.ReaderAt, dest io.Writer, opt UnpackOption) error {
	workDir, err := ioutil.TempDir(os.Getenv(envNydusWorkdir), "nydus-converter-")
	if err != nil {
		return errors.Wrap(err, "create work directory")
	}
	defer func() {
		os.RemoveAll(workDir)
	}()

	bootPath, blobPath := filepath.Join(workDir, bootstrapNameInTar), filepath.Join(workDir, blobNameInTar)
	if err = unpackNydusTar(ctx, bootPath, blobPath, ia); err != nil {
		return errors.Wrap(err, "unpack nydus tar")
	}

	tarPath := filepath.Join(workDir, "oci.tar")
	blobFifo, err := fifo.OpenFifo(ctx, tarPath, syscall.O_CREAT|syscall.O_RDONLY|syscall.O_NONBLOCK, 0644)
	if err != nil {
		return errors.Wrapf(err, "create fifo file")
	}
	defer blobFifo.Close()

	unpackErrChan := make(chan error)
	go func() {
		defer close(unpackErrChan)
		err := tool.Unpack(tool.UnpackOption{
			BuilderPath:   getBuilder(),
			BootstrapPath: bootPath,
			BlobPath:      blobPath,
			TarPath:       tarPath,
		})
		if err != nil {
			blobFifo.Close()
			unpackErrChan <- err
		}
	}()

	if _, err := io.Copy(dest, blobFifo); err != nil {
		if unpackErr := <-unpackErrChan; unpackErr != nil {
			return errors.Wrap(unpackErr, "unpack")
		}
		return errors.Wrap(err, "copy oci tar")
	}

	return nil
}
