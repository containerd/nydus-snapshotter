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
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"

	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/images/converter"
	"github.com/containerd/fifo"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"

	"github.com/containerd/nydus-snapshotter/pkg/converter/tool"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
)

const bootstrapNameInTar = "image.boot"
const blobNameInTar = "image.blob"

const envNydusBuilder = "NYDUS_BUILDER"
const envNydusWorkdir = "NYDUS_WORKDIR"

const configGCLabelKey = "containerd.io/gc.ref.content.config"

func getBuilder(specifiedPath string) string {
	if specifiedPath != "" {
		return specifiedPath
	}

	builderPath := os.Getenv(envNydusBuilder)
	if builderPath != "" {
		return builderPath
	}

	return "nydus-image"
}

func getWorkdir(specifiedPath string) string {
	if specifiedPath != "" {
		return specifiedPath
	}

	workdirPath := os.Getenv(envNydusWorkdir)
	if workdirPath != "" {
		return workdirPath
	}

	return os.TempDir()
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
// `blob_data | blob_tar_header | bootstrap_data | bootstrap_tar_header`
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
	workDir, err := ioutil.TempDir(getWorkdir(opt.WorkDir), "nydus-converter-")
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
		var err error

		defer func() {
			if err != nil {
				pr.CloseWithError(errors.Wrapf(err, "unpack to %s", sourceDir))
			} else {
				pr.Close()
			}
		}()

		if err = unpackOciTar(ctx, sourceDir, pr); err != nil {
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
			var err error

			defer func() {
				if err != nil {
					pw.CloseWithError(errors.Wrapf(err, "convert blob for %s", sourceDir))
				} else {
					pw.Close()
				}
			}()

			err = tool.Pack(tool.PackOption{
				BuilderPath: getBuilder(opt.BuilderPath),

				BlobPath:         blobPath,
				FsVersion:        opt.FsVersion,
				SourcePath:       sourceDir,
				ChunkDictPath:    opt.ChunkDictPath,
				PrefetchPatterns: opt.PrefetchPatterns,
				Compressor:       opt.Compressor,
			})
			if err != nil {
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

// Merge multiple nydus bootstraps (from every layer of image) to a final bootstrap.
func Merge(ctx context.Context, layers []Layer, dest io.Writer, opt MergeOption) error {
	workDir, err := ioutil.TempDir(getWorkdir(opt.WorkDir), "nydus-converter-")
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

				// Use the hex hash string of whole tar blob as the bootstrap name.
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
		BuilderPath: getBuilder(opt.BuilderPath),

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
			return errors.Wrap(err, "open target bootstrap")
		}
	}
	defer rc.Close()

	if _, err = io.Copy(dest, rc); err != nil {
		return errors.Wrap(err, "copy merged bootstrap")
	}

	return nil
}

func Unpack(ctx context.Context, ia content.ReaderAt, dest io.Writer, opt UnpackOption) error {
	workDir, err := ioutil.TempDir(getWorkdir(opt.WorkDir), "nydus-converter-")
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
			BuilderPath:   getBuilder(opt.BuilderPath),
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

// LayerConvertFunc returns a function which converts an OCI image layer to a nydus blob layer, and set the media type to "application/vnd.oci.image.layer.nydus.blob.v1".
func LayerConvertFunc(opt PackOption) converter.ConvertFunc {
	return func(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
		if !images.IsLayerType(desc.MediaType) {
			return nil, nil
		}

		ra, err := cs.ReaderAt(ctx, desc)
		if err != nil {
			return nil, errors.Wrap(err, "get source blob reader")
		}
		defer ra.Close()
		rdr := io.NewSectionReader(ra, 0, ra.Size())

		ref := fmt.Sprintf("convert-nydus-from-%s", desc.Digest)
		dst, err := content.OpenWriter(ctx, cs, content.WithRef(ref))
		if err != nil {
			return nil, errors.Wrap(err, "open blob writer")
		}
		defer dst.Close()

		tr, err := compression.DecompressStream(rdr)
		if err != nil {
			return nil, errors.Wrap(err, "decompress blob stream")
		}

		digester := digest.SHA256.Digester()
		pr, pw := io.Pipe()
		tw, err := Pack(ctx, io.MultiWriter(pw, digester.Hash()), opt)
		if err != nil {
			return nil, errors.Wrap(err, "pack tar to nydus")
		}

		go func() {
			defer pw.Close()
			if _, err := io.Copy(tw, tr); err != nil {
				pw.CloseWithError(err)
				return
			}
			if err := tr.Close(); err != nil {
				pw.CloseWithError(err)
				return
			}
			if err := tw.Close(); err != nil {
				pw.CloseWithError(err)
				return
			}
		}()

		if err := content.Copy(ctx, dst, pr, 0, ""); err != nil {
			return nil, errors.Wrap(err, "copy nydus blob to content store")
		}

		blobDigest := digester.Digest()
		info, err := cs.Info(ctx, blobDigest)
		if err != nil {
			return nil, errors.Wrapf(err, "get blob info %s", blobDigest)
		}

		newDesc := ocispec.Descriptor{
			Digest:    blobDigest,
			Size:      info.Size,
			MediaType: MediaTypeNydusBlob,
			Annotations: map[string]string{
				// Use `containerd.io/uncompressed` to generate DiffID of
				// layer defined in OCI spec.
				LayerAnnotationUncompressed: blobDigest.String(),
				LayerAnnotationNydusBlob:    "true",
			},
		}

		if opt.Backend != nil {
			blobRa, err := cs.ReaderAt(ctx, newDesc)
			if err != nil {
				return nil, errors.Wrap(err, "get nydus blob reader")
			}
			blobReader := io.NewSectionReader(blobRa, 0, blobRa.Size())

			if err := opt.Backend.Push(ctx, blobReader, blobDigest); err != nil {
				return nil, errors.Wrap(err, "push to storage backend")
			}
		}

		return &newDesc, nil
	}
}

// ConvertHookFunc returns a function which will be used as a callback called for each blob after conversion is done.
// The function only hooks the manifest conversion.
// The function merges all the nydus blob layers into a nydus bootstrap layer and modify the config.
func ConvertHookFunc(opt PackOption) converter.ConvertHookFunc {
	return func(ctx context.Context, cs content.Store, orgDesc ocispec.Descriptor, newDesc *ocispec.Descriptor) (*ocispec.Descriptor, error) {
		if !images.IsManifestType(newDesc.MediaType) {
			// Only need to hook manifest conversion
			return newDesc, nil
		}

		// Convert manifest
		var manifest ocispec.Manifest
		manifestDesc := *newDesc
		labels, err := readJSON(ctx, cs, &manifest, manifestDesc)
		if err != nil {
			return nil, errors.Wrap(err, "read manifest json")
		}

		// Append bootstrap layer to manifest.
		bootstrapDesc, err := mergeLayers(ctx, cs, manifest.Layers, MergeOption{
			BuilderPath:   opt.BuilderPath,
			WorkDir:       opt.WorkDir,
			ChunkDictPath: opt.ChunkDictPath,
			WithTar:       true,
		}, opt.FsVersion)
		if err != nil {
			return nil, errors.Wrap(err, "merge nydus layers")
		}
		bootstrapDiffID := digest.Digest(bootstrapDesc.Annotations[LayerAnnotationUncompressed])

		if opt.Backend != nil {
			// Only append nydus bootstrap layer into manifest, and do not put nydus
			// blob layer into manifest if blob storage backend is specified.
			manifest.Layers = []ocispec.Descriptor{*bootstrapDesc}
		} else {
			manifest.Layers = append(manifest.Layers, *bootstrapDesc)
		}

		// Update the gc label of bootstrap layer
		bootstrapGCLabelKey := fmt.Sprintf("containerd.io/gc.ref.content.l.%d", len(manifest.Layers)-1)
		labels[bootstrapGCLabelKey] = bootstrapDesc.Digest.String()

		// Remove useless annotation.
		for _, layer := range manifest.Layers {
			delete(layer.Annotations, LayerAnnotationUncompressed)
		}

		// Update diff ids in image config.
		var config ocispec.Image
		configLabels, err := readJSON(ctx, cs, &config, manifest.Config)
		if err != nil {
			return nil, errors.Wrap(err, "read image config")
		}
		if opt.Backend != nil {
			config.RootFS.DiffIDs = []digest.Digest{bootstrapDiffID}
		} else {
			config.RootFS.DiffIDs = append(config.RootFS.DiffIDs, bootstrapDiffID)
		}

		// Update image config in content store.
		newConfigDesc, err := writeJSON(ctx, cs, config, manifest.Config, configLabels)
		if err != nil {
			return nil, errors.Wrap(err, "write image config")
		}
		manifest.Config = *newConfigDesc
		// Update the config gc label
		labels[configGCLabelKey] = newConfigDesc.Digest.String()

		// Update image manifest in content store.
		newManifestDesc, err := writeJSON(ctx, cs, manifest, manifestDesc, labels)
		if err != nil {
			return nil, errors.Wrap(err, "write manifest")
		}

		return newManifestDesc, nil
	}
}

// mergeLayers merege a list of ndyus blob layer into a nydus bootstrap layer.
// The media type of the nydus bootstrap layer is "application/vnd.oci.image.layer.v1.tar+gzip".
func mergeLayers(ctx context.Context, cs content.Store, descs []ocispec.Descriptor, opt MergeOption, fsVersion string) (*ocispec.Descriptor, error) {
	// Extracts nydus bootstrap from nydus format for each layer.
	layers := []Layer{}
	blobIDs := []string{}

	var chainID digest.Digest
	for _, blobDesc := range descs {
		ra, err := cs.ReaderAt(ctx, blobDesc)
		if err != nil {
			return nil, errors.Wrapf(err, "get reader for blob %q", blobDesc.Digest)
		}
		defer ra.Close()
		blobIDs = append(blobIDs, blobDesc.Digest.Hex())
		layers = append(layers, Layer{
			Digest:   blobDesc.Digest,
			ReaderAt: ra,
		})
		if chainID == "" {
			chainID = identity.ChainID([]digest.Digest{blobDesc.Digest})
		} else {
			chainID = identity.ChainID([]digest.Digest{chainID, blobDesc.Digest})
		}
	}

	// Merge all nydus bootstraps into a final nydus bootstrap.
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		if err := Merge(ctx, layers, pw, opt); err != nil {
			pw.CloseWithError(errors.Wrapf(err, "merge nydus bootstrap"))
		}
	}()

	// Compress final nydus bootstrap to tar.gz and write into content store.
	cw, err := content.OpenWriter(ctx, cs, content.WithRef("nydus-merge-"+chainID.String()))
	if err != nil {
		return nil, errors.Wrap(err, "open content store writer")
	}
	defer cw.Close()

	gw := gzip.NewWriter(cw)
	uncompressedDgst := digest.SHA256.Digester()
	compressed := io.MultiWriter(gw, uncompressedDgst.Hash())
	if _, err := io.Copy(compressed, pr); err != nil {
		return nil, errors.Wrapf(err, "copy bootstrap targz into content store")
	}
	if err := gw.Close(); err != nil {
		return nil, errors.Wrap(err, "close gzip writer")
	}

	compressedDgst := cw.Digest()
	if err := cw.Commit(ctx, 0, compressedDgst, content.WithLabels(map[string]string{
		LayerAnnotationUncompressed: uncompressedDgst.Digest().String(),
	})); err != nil {
		if !errdefs.IsAlreadyExists(err) {
			return nil, errors.Wrap(err, "commit to content store")
		}
	}
	if err := cw.Close(); err != nil {
		return nil, errors.Wrap(err, "close content store writer")
	}

	info, err := cs.Info(ctx, compressedDgst)
	if err != nil {
		return nil, errors.Wrap(err, "get info from content store")
	}

	blobIDsBytes, err := json.Marshal(blobIDs)
	if err != nil {
		return nil, errors.Wrap(err, "marshal blob ids")
	}

	desc := ocispec.Descriptor{
		Digest:    compressedDgst,
		Size:      info.Size,
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Annotations: map[string]string{
			LayerAnnotationUncompressed: uncompressedDgst.Digest().String(),
			LayerAnnotationFSVersion:    fsVersion,
			// Use this annotation to identify nydus bootstrap layer.
			LayerAnnotationNydusBootstrap: "true",
			// Track all blob digests for nydus snapshotter.
			LayerAnnotationNydusBlobIDs: string(blobIDsBytes),
		},
	}

	return &desc, nil
}
