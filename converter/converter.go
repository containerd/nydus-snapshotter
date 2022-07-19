/* SPDX-License-Identifier: Apache-2.0 */
/* Copyright (c) 2022. Alibaba Cloud, Ant Group. All rights reserved. */

package converter

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/pkg/cri/constants"
	"github.com/containerd/containerd/platforms"
	nydusify "github.com/containerd/nydus-snapshotter/pkg/converter"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pingcap/failpoint"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("module", "converter")

const (
	fsVersion = "6"
	// use none compressor (image/image.boot) to save CPU resources
	compressor             = "none"
	maxConcurrentDownloads = 3
	MaxTimeOut             = 2 * time.Hour
)

var _ Converter = &LocalConverter{}

type Converter interface {
	Init(ctx context.Context) error
	Convert(ctx context.Context, source string, manifestDigest digest.Digest, currentLayerDigest digest.Digest, blobPath string) error
	Merge(ctx context.Context, blobs []string, bootstrapPath string) error
	BlobDir() string
	DeleteImage(any interface{})
	DeleteManifest(any interface{})
}

type LocalConverter struct {
	client *containerd.Client
	Config *Config
	// key is manifest digest, value is layers' descriptors (manifest.Layers), need cleanup when merge
	// the key will be deleted after merging final bootstrap
	ManifestLayersMap sync.Map
	// key is image name, value is image interface
	// the key will be deleted after merging final bootstrap
	ImageMap sync.Map
}

type Config struct {
	ContainerdAddress string
	BlobDir           string
	BuilderPath       string
	Timeout           *time.Duration
}

func New(cfg *Config) (Converter, error) {
	conv := &LocalConverter{
		client:            nil,
		Config:            cfg,
		ManifestLayersMap: sync.Map{},
		ImageMap:          sync.Map{},
	}
	return conv, conv.Init(context.Background())
}

func (cvt *LocalConverter) DeleteImage(any interface{}) {
	cvt.ImageMap.Delete(any)
}

func (cvt *LocalConverter) DeleteManifest(any interface{}) {
	cvt.ImageMap.Delete(any)
}

func (cvt *LocalConverter) BlobDir() string {
	return cvt.Config.BlobDir
}

func (cvt *LocalConverter) Init(ctx context.Context) error {
	if cvt.client != nil {
		return nil
	}
	client, err := containerd.New(
		cvt.Config.ContainerdAddress,
		containerd.WithDefaultNamespace(constants.K8sContainerdNamespace),
	)
	if err != nil {
		return err
	}
	cvt.client = client
	return nil
}

func (cvt *LocalConverter) Convert(ctx context.Context, source string, manifestDigest digest.Digest, currentLayerDigest digest.Digest, blobPath string) error {
	if err := cvt.Init(ctx); err != nil {
		return err
	}
	ctx, done, err := cvt.client.WithLease(ctx)
	if err != nil {
		return errors.Wrap(err, "create lease")
	}
	defer done(ctx)

	// check content if current layer exist
	cs := cvt.client.ContentStore()
	if _, err = cs.Info(ctx, currentLayerDigest); err != nil {
		// if ErrNotFound, pull image
		if !errdefs.IsNotFound(err) {
			return errors.Wrap(err, "get info of layer")
		}
		logger.Infof("pulling image %s", source)
		start := time.Now()
		failpoint.Inject("pull-image-failed", func() error {
			return fmt.Errorf("pull image failed")
		})
		if err := cvt.Pull(ctx, source); err != nil {
			return errors.Wrap(err, "pull image")
		}
		logger.Infof("pulled image %s, elapse %s", source, time.Since(start))
	}
	image, err := cvt.client.ImageService().Get(ctx, source)
	failpoint.Inject("get-image-failed", func() {
		err = fmt.Errorf("failed to get image %s from image store", source)
	})
	if err != nil {
		return errors.Wrapf(err, "failed to get image %s from image store", source)
	}
	containerdImage, _ := cvt.ImageMap.LoadOrStore(source, containerd.NewImageWithPlatform(cvt.client, image, platforms.Default()))

	// the fast path of getting all the layers descriptors corresponding to the manifest,
	// to avoid reading content for each layer.
	descs, ok := cvt.ManifestLayersMap.Load(manifestDigest)
	if !ok {
		manifest, err := GetManifestByDigest(ctx, cs, manifestDigest, containerdImage.(containerd.Image).Target())
		if err != nil {
			return err
		}
		// use "=" instead of ":=" to avoid local variable overwriting
		descs, err = GetLayersByManifestDescriptor(ctx, cs, *manifest)
		if err != nil {
			return err
		}
		cvt.ManifestLayersMap.Store(manifestDigest, descs)
	}

	for _, layerDesc := range descs.([]ocispec.Descriptor) {
		if layerDesc.Digest != currentLayerDigest {
			continue
		}

		logger.Infof("converting layer %s in image %s", currentLayerDigest, source)
		start := time.Now()
		if err := convertToNydusLayer(ctx, cs, layerDesc, nydusify.PackOption{
			FsVersion:   fsVersion,
			Compressor:  compressor,
			BuilderPath: cvt.Config.BuilderPath,
			WorkDir:     cvt.Config.BlobDir,
			Timeout:     cvt.Config.Timeout,
		}, blobPath); err != nil {
			return errors.Wrap(err, "convert oci layer to nydus blob")
		}
		failpoint.Inject("convery-nydus-blob-failed", func() error {
			return fmt.Errorf("convert oci layer to nydus blob")
		})
		logger.Infof("converted layer %s in image %s, elapse %s", currentLayerDigest, source, time.Since(start))
		return nil
	}

	return fmt.Errorf("failed to match current layer digest %v in layers descs %#v", currentLayerDigest, descs)
}

func convertToNydusLayer(ctx context.Context, cs content.Store, desc ocispec.Descriptor, opt nydusify.PackOption, blobPath string) error {
	if !images.IsLayerType(desc.MediaType) {
		logger.Infof("MediaType of desc %+v is not layer", desc)
		return nil
	}

	ra, err := cs.ReaderAt(ctx, desc)
	if err != nil {
		return errors.Wrap(err, "get source blob reader")
	}
	defer ra.Close()
	rdr := io.NewSectionReader(ra, 0, ra.Size())

	tr, err := compression.DecompressStream(rdr)
	if err != nil {
		return errors.Wrap(err, "decompress blob stream")
	}

	digester := digest.SHA256.Digester()
	pr, pw := io.Pipe()
	tw, err := nydusify.Pack(ctx, io.MultiWriter(pw, digester.Hash()), opt)
	if err != nil {
		return errors.Wrap(err, "pack tar to nydus")
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

	blobFile, err := ioutil.TempFile(path.Dir(blobPath), "converting-")
	if err != nil {
		return errors.Wrap(err, "create temp file for converting blob")
	}
	defer func() {
		if err != nil {
			os.Remove(blobFile.Name())
		}
		blobFile.Close()
	}()

	if _, err := io.Copy(blobFile, pr); err != nil {
		return errors.Wrap(err, "copy nydus blob to blobdir")
	}
	if err := os.Rename(blobFile.Name(), blobPath); err != nil {
		return errors.Wrap(err, "rename temp file as blob file")
	}

	return nil
}

// The final uncompressed nydus bootstrap will be merged from all nydus blobs when Prepare() for upper layer,
// with the path /PATH/TO/SNAPSHOT_ID/fs/image/image.boot
func (cvt *LocalConverter) Merge(ctx context.Context, blobs []string, bootstrapPath string) error {
	// Extracts nydus bootstrap from nydus format for each layer.
	opt := nydusify.MergeOption{
		BuilderPath: cvt.Config.BuilderPath,
		WorkDir:     cvt.Config.BlobDir,
		Timeout:     cvt.Config.Timeout,
	}
	layers := []nydusify.BlobLayer{}
	// traverse blobs in reverse order.
	for i := len(blobs) - 1; i >= 0; i-- {
		blob := blobs[i]
		ra, err := local.OpenReader(filepath.Join(opt.WorkDir, blob))
		if err != nil {
			return errors.Wrapf(err, "get reader for blob %q", blob)
		}
		defer ra.Close()
		layers = append(layers, nydusify.BlobLayer{
			Name:     blob,
			ReaderAt: ra,
		})
	}

	// Merge all nydus bootstraps into a final nydus bootstrap.
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		if err := nydusify.Merge(ctx, layers, pw, opt); err != nil {
			pw.CloseWithError(errors.Wrapf(err, "merge nydus bootstrap"))
		}
	}()

	bootstrapFile, err := ioutil.TempFile(path.Dir(bootstrapPath), "merging-")
	if err != nil {
		return errors.Wrap(err, "create temp file for merging bootstrap")
	}
	defer func() {
		if err != nil {
			os.Remove(bootstrapFile.Name())
		}
		bootstrapFile.Close()
	}()

	uncompressedDgst := digest.SHA256.Digester()
	uncompressed := io.MultiWriter(bootstrapFile, uncompressedDgst.Hash())
	if _, err := io.Copy(uncompressed, pr); err != nil {
		return errors.Wrapf(err, "copy uncompressed bootstrap to %s", bootstrapFile.Name())
	}
	if err := os.Rename(bootstrapFile.Name(), bootstrapPath); err != nil {
		return errors.Wrap(err, "rename temp file as bootstrap file")
	}

	return nil
}

func (cvt *LocalConverter) Pull(ctx context.Context, ref string) error {
	if err := cvt.Init(ctx); err != nil {
		return err
	}
	resolver := NewResolver(NewDockerConfigCredFunc())

	opts := []containerd.RemoteOpt{
		containerd.WithPlatformMatcher(platforms.Default()),
		containerd.WithMaxConcurrentDownloads(maxConcurrentDownloads),
		containerd.WithImageHandler(images.HandlerFunc(
			func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
				if images.IsLayerType(desc.MediaType) {
					logger.Debugf("pulling layer %s", desc.Digest)
				}
				return nil, nil
			},
		)),
		containerd.WithResolver(resolver),
	}

	// Pull the source image from remote registry.
	_, err := cvt.client.Fetch(ctx, ref, opts...)
	if err != nil {
		return errors.Wrap(err, "pull source image")
	}

	return nil
}
