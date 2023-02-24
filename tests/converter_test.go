/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tests

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/containerd/containerd"
	containerdconverter "github.com/containerd/containerd/images/converter"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/platforms"
	"github.com/opencontainers/go-digest"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/nydus-snapshotter/pkg/backend"
	"github.com/containerd/nydus-snapshotter/pkg/converter"
)

const envNydusdPath = "NYDUS_NYDUSD"

func hugeString(mb int) string {
	var buf strings.Builder
	size := mb * 1024 * 1024
	seqSize := 512 * 1024
	buf.Grow(size)

	seq := size / seqSize

	for i := 0; i < seq; i++ {
		data := make([]byte, seqSize)
		if i%2 == 0 {
			_, err := rand.Read(data)
			if err != nil {
				log.L.WithError(err)
			}
		}
		buf.Write(data)
	}

	return buf.String()
}

func dropCache(t *testing.T) {
	cmd := exec.Command("sh", "-c", "echo 3 > /proc/sys/vm/drop_caches")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())
}

func ensureFile(t *testing.T, name string) {
	_, err := os.Stat(name)
	require.NoError(t, err)
}

func ensureNoFile(t *testing.T, name string) {
	_, err := os.Stat(name)
	require.True(t, errors.Is(err, os.ErrNotExist))
}

func writeFileToTar(t *testing.T, tw *tar.Writer, name string, data string) {
	u, err := user.Current()
	require.NoError(t, err)
	g, err := user.LookupGroupId(u.Uid)
	require.NoError(t, err)

	hdr := &tar.Header{
		Name:  name,
		Mode:  0444,
		Size:  int64(len(data)),
		Uname: u.Name,
		Gname: g.Name,
	}
	err = tw.WriteHeader(hdr)
	require.NoError(t, err)

	_, err = io.Copy(tw, bytes.NewReader([]byte(data)))
	require.Nil(t, err)
	require.NoError(t, err)
}

func writeDirToTar(t *testing.T, tw *tar.Writer, name string) {
	u, err := user.Current()
	require.NoError(t, err)
	g, err := user.LookupGroupId(u.Uid)
	require.NoError(t, err)

	hdr := &tar.Header{
		Name:     name,
		Mode:     0444,
		Typeflag: tar.TypeDir,
		Uname:    u.Name,
		Gname:    g.Name,
	}
	err = tw.WriteHeader(hdr)
	require.NoError(t, err)
}

func writeToFile(t *testing.T, reader io.Reader, fileName string) {
	file, err := os.Create(fileName)
	require.NoError(t, err)
	defer file.Close()

	_, err = io.Copy(file, reader)
	require.NoError(t, err)
}

func buildChunkDictTar(t *testing.T, n int) io.ReadCloser {
	pr, pw := io.Pipe()
	tw := tar.NewWriter(pw)

	go func() {
		defer pw.Close()

		writeDirToTar(t, tw, "dir-1")

		for i := 1; i < n; i++ {
			writeFileToTar(t, tw, fmt.Sprintf("dir-1/file-%d", i), fmt.Sprintf("lower-file-%d", i))
		}

		require.NoError(t, tw.Close())
	}()

	return pr
}

func buildOCILowerTar(t *testing.T, n int) (io.ReadCloser, map[string]string) {
	fileTree := map[string]string{}

	pr, pw := io.Pipe()
	tw := tar.NewWriter(pw)

	go func() {
		defer pw.Close()

		writeDirToTar(t, tw, "dir-1")
		fileTree["dir-1"] = ""

		for i := 1; i < n; i++ {
			writeFileToTar(t, tw, fmt.Sprintf("dir-1/file-%d", i), fmt.Sprintf("lower-file-%d", i))
			fileTree[fmt.Sprintf("dir-1/file-%d", i)] = fmt.Sprintf("lower-file-%d", i)
		}

		writeDirToTar(t, tw, "dir-2")
		fileTree["dir-2"] = ""

		writeFileToTar(t, tw, "dir-2/file-1", "lower-file-1")
		fileTree["dir-2/file-1"] = "lower-file-1"

		require.NoError(t, tw.Close())
	}()

	return pr, fileTree
}

func buildOCIUpperTar(t *testing.T, teePath string, lowerFileTree map[string]string) (io.ReadCloser, map[string]string) {
	if lowerFileTree == nil {
		lowerFileTree = map[string]string{}
	}

	hugeStr := hugeString(3)
	pr, pw := io.Pipe()

	go func() {
		tw := tar.NewWriter(pw)
		defer pw.Close()

		if len(teePath) > 0 {
			teew, err := os.OpenFile(teePath, os.O_WRONLY|os.O_CREATE, 0644)
			require.NoError(t, err)
			defer teew.Close()

			tw = tar.NewWriter(io.MultiWriter(pw, teew))
		}

		writeDirToTar(t, tw, "dir-1")
		lowerFileTree["dir-1"] = ""

		writeFileToTar(t, tw, "dir-1/.wh.file-1", "")
		delete(lowerFileTree, "dir-1/file-1")

		writeDirToTar(t, tw, "dir-2")
		lowerFileTree["dir-2"] = ""

		writeFileToTar(t, tw, "dir-2/.wh..wh..opq", "")
		for k := range lowerFileTree {
			if strings.HasPrefix(k, "dir-2/") {
				delete(lowerFileTree, k)
			}
		}

		writeFileToTar(t, tw, "dir-2/file-1", hugeStr)
		lowerFileTree["dir-2/file-1"] = hugeStr

		writeFileToTar(t, tw, "dir-2/file-2", "upper-file-2")
		lowerFileTree["dir-2/file-2"] = "upper-file-2"

		writeFileToTar(t, tw, "dir-2/file-3", "upper-file-3")
		lowerFileTree["dir-2/file-3"] = "upper-file-3"

		require.NoError(t, tw.Close())
	}()

	return pr, lowerFileTree
}

func packLayer(t *testing.T, source io.ReadCloser, chunkDict, workDir string, fsVersion string) (string, digest.Digest) {
	var data bytes.Buffer
	writer := io.Writer(&data)

	twc, err := converter.Pack(context.TODO(), writer, converter.PackOption{
		ChunkDictPath: chunkDict,
		FsVersion:     fsVersion,
	})
	require.NoError(t, err)

	_, err = io.Copy(twc, source)
	require.NoError(t, err)
	err = twc.Close()
	require.NoError(t, err)

	blobDigester := digest.Canonical.Digester()
	_, err = blobDigester.Hash().Write(data.Bytes())
	require.NoError(t, err)
	blobDigest := blobDigester.Digest()

	tarBlobFilePath := filepath.Join(workDir, blobDigest.Hex())
	writeToFile(t, bytes.NewReader(data.Bytes()), tarBlobFilePath)

	return tarBlobFilePath, blobDigest
}

func packLayerRef(t *testing.T, gzipSource io.ReadCloser, workDir string) (string, digest.Digest, digest.Digest) {
	var blobMetaData bytes.Buffer
	blobMetaWriter := io.Writer(&blobMetaData)

	pr, pw := io.Pipe()
	gzipBlobDigester := digest.Canonical.Digester()
	hw := io.MultiWriter(pw, gzipBlobDigester.Hash())
	go func() {
		defer pw.Close()
		_, err := io.Copy(hw, gzipSource)
		require.NoError(t, err)
	}()

	twc, err := converter.Pack(context.TODO(), blobMetaWriter, converter.PackOption{
		OCIRef: true,
	})
	require.NoError(t, err)

	_, err = io.Copy(twc, pr)
	require.NoError(t, err)
	err = twc.Close()
	require.NoError(t, err)

	blobMetaDigester := digest.Canonical.Digester()
	_, err = blobMetaDigester.Hash().Write(blobMetaData.Bytes())
	require.NoError(t, err)
	blobMetaDigest := blobMetaDigester.Digest()

	tarBlobMetaFilePath := filepath.Join(workDir, blobMetaDigest.Hex())
	writeToFile(t, bytes.NewReader(blobMetaData.Bytes()), tarBlobMetaFilePath)

	gzipBlobDigest := gzipBlobDigester.Digest()

	return tarBlobMetaFilePath, blobMetaDigest, gzipBlobDigest
}

func unpackLayer(t *testing.T, workDir string, ra content.ReaderAt) (string, digest.Digest) {
	var data bytes.Buffer
	writer := io.Writer(&data)

	err := converter.Unpack(context.TODO(), ra, writer, converter.UnpackOption{})
	require.NoError(t, err)

	digester := digest.Canonical.Digester()
	_, err = digester.Hash().Write(data.Bytes())
	require.NoError(t, err)
	digest := digester.Digest()

	tarPath := filepath.Join(workDir, digest.Hex())
	writeToFile(t, bytes.NewReader(data.Bytes()), tarPath)

	return tarPath, digest
}

func verify(t *testing.T, workDir string, expectedFileTree map[string]string) {
	mountDir := filepath.Join(workDir, "mnt")
	blobDir := filepath.Join(workDir, "blobs")
	nydusdPath := os.Getenv(envNydusdPath)
	if nydusdPath == "" {
		nydusdPath = "nydusd"
	}
	config := NydusdConfig{
		EnablePrefetch: false,
		NydusdPath:     nydusdPath,
		BootstrapPath:  filepath.Join(workDir, "bootstrap"),
		ConfigPath:     filepath.Join(workDir, "nydusd-config.fusedev.json"),
		BackendType:    "localfs",
		BackendConfig:  fmt.Sprintf(`{"dir": "%s"}`, blobDir),
		BlobCacheDir:   filepath.Join(workDir, "cache"),
		APISockPath:    filepath.Join(workDir, "nydusd-api.sock"),
		MountPath:      mountDir,
		Mode:           "direct",
		DigestValidate: false,
	}

	nydusd, err := NewNydusd(config)
	require.NoError(t, err)
	err = nydusd.Mount()
	require.NoError(t, err)
	defer func() {
		if err := nydusd.Umount(); err != nil {
			log.L.WithError(err).Errorf("umount")
		}
	}()

	actualFileTree := map[string]string{}
	err = filepath.WalkDir(mountDir, func(path string, entry fs.DirEntry, err error) error {
		require.Nil(t, err)
		info, err := entry.Info()
		require.NoError(t, err)

		targetPath, err := filepath.Rel(mountDir, path)
		require.NoError(t, err)

		if targetPath == "." {
			return nil
		}

		data := ""
		if !info.IsDir() {
			file, err := os.Open(path)
			require.NoError(t, err)
			defer file.Close()
			_data, err := io.ReadAll(file)
			require.NoError(t, err)
			data = string(_data)
		}
		actualFileTree[targetPath] = data

		return nil
	})
	require.NoError(t, err)

	require.Equal(t, expectedFileTree, actualFileTree)
}

func buildChunkDict(t *testing.T, workDir, fsVersion string, n int) (string, string) {
	dictOCITarReader := buildChunkDictTar(t, n)

	blobDir := filepath.Join(workDir, "blobs")
	nydusTarPath, lowerNydusBlobDigest := packLayer(t, dictOCITarReader, "", blobDir, fsVersion)
	ra, err := local.OpenReader(nydusTarPath)
	require.NoError(t, err)
	defer ra.Close()

	layers := []converter.Layer{
		{
			Digest:   lowerNydusBlobDigest,
			ReaderAt: ra,
		},
	}

	bootstrapPath := filepath.Join(workDir, "dict-bootstrap")
	file, err := os.Create(bootstrapPath)
	require.NoError(t, err)
	defer file.Close()

	blobDigests, err := converter.Merge(context.TODO(), layers, file, converter.MergeOption{})
	require.NoError(t, err)
	require.Equal(t, []digest.Digest{lowerNydusBlobDigest}, blobDigests)

	dictBlobPath := ""
	err = filepath.WalkDir(blobDir, func(path string, entry fs.DirEntry, err error) error {
		require.NoError(t, err)
		if path == blobDir {
			return nil
		}
		dictBlobPath = path
		return nil
	})
	require.NoError(t, err)

	return bootstrapPath, filepath.Base(dictBlobPath)
}

func TestPack(t *testing.T) {
	testPack(t, "5")
	testPack(t, "6")
}

func testPack(t *testing.T, fsVersion string) {
	workDir, err := os.MkdirTemp("", "nydus-converter-test-")
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	lowerOCITarReader, expectedLowerFileTree := buildOCILowerTar(t, 100)
	upperOCITarReader, expectedOverlayFileTree := buildOCIUpperTar(t, "", expectedLowerFileTree)

	blobDir := filepath.Join(workDir, "blobs")
	err = os.MkdirAll(blobDir, 0755)
	require.NoError(t, err)

	cacheDir := filepath.Join(workDir, "cache")
	err = os.MkdirAll(cacheDir, 0755)
	require.NoError(t, err)

	mountDir := filepath.Join(workDir, "mnt")
	err = os.MkdirAll(mountDir, 0755)
	require.NoError(t, err)

	chunkDictBootstrapPath, chunkDictBlobHash := buildChunkDict(t, workDir, fsVersion, 100)

	lowerNydusTarPath, lowerNydusBlobDigest := packLayer(t, lowerOCITarReader, chunkDictBootstrapPath, blobDir, fsVersion)
	upperNydusTarPath, upperNydusBlobDigest := packLayer(t, upperOCITarReader, chunkDictBootstrapPath, blobDir, fsVersion)

	lowerTarRa, err := local.OpenReader(lowerNydusTarPath)
	require.NoError(t, err)
	defer lowerTarRa.Close()

	upperTarRa, err := local.OpenReader(upperNydusTarPath)
	require.NoError(t, err)
	defer upperTarRa.Close()

	layers := []converter.Layer{
		{
			Digest:   lowerNydusBlobDigest,
			ReaderAt: lowerTarRa,
		},
		{
			Digest:   upperNydusBlobDigest,
			ReaderAt: upperTarRa,
		},
	}

	bootstrapPath := filepath.Join(workDir, "bootstrap")
	file, err := os.Create(bootstrapPath)
	require.NoError(t, err)
	defer file.Close()

	blobDigests, err := converter.Merge(context.TODO(), layers, file, converter.MergeOption{
		ChunkDictPath: chunkDictBootstrapPath,
	})
	require.NoError(t, err)
	chunkDictBlobDigest := digest.NewDigestFromHex(string(digest.SHA256), chunkDictBlobHash)
	expectedBlobDigests := []digest.Digest{chunkDictBlobDigest, upperNydusBlobDigest}
	require.Equal(t, expectedBlobDigests, blobDigests)

	verify(t, workDir, expectedOverlayFileTree)
	dropCache(t)
	verify(t, workDir, expectedOverlayFileTree)

	ensureFile(t, filepath.Join(cacheDir, chunkDictBlobHash)+".chunk_map")
	ensureNoFile(t, filepath.Join(cacheDir, lowerNydusBlobDigest.Hex())+".chunk_map")
	ensureFile(t, filepath.Join(cacheDir, upperNydusBlobDigest.Hex())+".chunk_map")
}

// sudo go test -v -count=1 -run TestPackRef ./tests
func TestPackRef(t *testing.T) {
	if os.Getenv("TEST_PACK_REF") == "" {
		t.Skip("skip TestPackRef test until new nydus-image/nydusd release")
	}

	workDir, err := os.MkdirTemp("", "nydus-converter-test-")
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	blobDir := filepath.Join(workDir, "blobs")
	err = os.MkdirAll(blobDir, 0755)
	require.NoError(t, err)

	cacheDir := filepath.Join(workDir, "cache")
	err = os.MkdirAll(cacheDir, 0755)
	require.NoError(t, err)

	mountDir := filepath.Join(workDir, "mnt")
	err = os.MkdirAll(mountDir, 0755)
	require.NoError(t, err)

	lowerOCITarReader, expectedLowerFileTree := buildOCILowerTar(t, 500)
	defer lowerOCITarReader.Close()

	var gzipData bytes.Buffer
	gzipWriter := gzip.NewWriter(&gzipData)
	_, err = io.Copy(gzipWriter, lowerOCITarReader)
	require.NoError(t, err)
	gzipWriter.Close()
	dupGzipData := gzipData

	gzipReader := io.NopCloser(&gzipData)
	lowerNydusBlobPath, lowerNydusBlobDigest, lowerGzipBlobDigest := packLayerRef(t, gzipReader, blobDir)

	writeToFile(t, &dupGzipData, path.Join(blobDir, lowerGzipBlobDigest.Hex()))

	lowerNydusBlobRa, err := local.OpenReader(lowerNydusBlobPath)
	require.NoError(t, err)
	defer lowerNydusBlobRa.Close()

	// Check uncompressed bootstrap digest in TOC
	bootstrapDigester := digest.Canonical.Digester()
	bootstrapTOC, err := converter.UnpackEntry(lowerNydusBlobRa, converter.EntryBootstrap, bootstrapDigester.Hash())
	require.NoError(t, err)
	require.Equal(t, bootstrapTOC.GetUncompressedDigest(), bootstrapDigester.Digest().Hex())

	// Check uncompressed blob meta digest in TOC
	blobMetaDigester := digest.Canonical.Digester()
	blobMetaTOC, err := converter.UnpackEntry(lowerNydusBlobRa, converter.EntryBlobMeta, blobMetaDigester.Hash())
	require.NoError(t, err)
	require.Equal(t, blobMetaTOC.GetUncompressedDigest(), blobMetaDigester.Digest().Hex())

	layers := []converter.Layer{
		{
			Digest:         lowerNydusBlobDigest,
			OriginalDigest: &lowerGzipBlobDigest,
			ReaderAt:       lowerNydusBlobRa,
		},
	}

	bootstrapPath := filepath.Join(workDir, "bootstrap")
	file, err := os.Create(bootstrapPath)
	require.NoError(t, err)
	defer file.Close()

	blobDigests, err := converter.Merge(context.TODO(), layers, file, converter.MergeOption{
		OCIRef: true,
	})
	require.NoError(t, err)

	require.Equal(t, []digest.Digest{lowerGzipBlobDigest}, blobDigests)

	verify(t, workDir, expectedLowerFileTree)
}

// sudo go test -v -count=1 -run TestUnpack ./tests
func TestUnpack(t *testing.T) {
	testUnpack(t, "5")
	testUnpack(t, "6")
}

func testUnpack(t *testing.T, fsVersion string) {
	workDir, err := os.MkdirTemp("", "nydus-converter-test-")
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	ociTar := filepath.Join(workDir, "oci.tar")
	ociTarReader, _ := buildOCIUpperTar(t, ociTar, nil)
	nydusTar, _ := packLayer(t, ociTarReader, "", workDir, fsVersion)

	tarTa, err := local.OpenReader(nydusTar)
	require.NoError(t, err)
	defer tarTa.Close()

	_, newTarDigest := unpackLayer(t, workDir, tarTa)

	ociTarReader, err = os.OpenFile(ociTar, os.O_RDONLY, 0644)
	require.NoError(t, err)
	ociTarDigest, err := digest.Canonical.FromReader(ociTarReader)
	require.NoError(t, err)

	require.Equal(t, ociTarDigest, newTarDigest)
}

type ConvertTestOption struct {
	t                    *testing.T
	fsVersion            string
	backend              converter.Backend
	disableCheck         bool
	beforeConversionHook func() error
	afterConversionHook  func() error
}

// sudo go test -v -count=1 -run TestImageConvert ./tests
func TestImageConvert(t *testing.T) {
	for _, fsVersion := range []string{"5", "6"} {
		testImageConvertNoBackend(t, fsVersion)
		testImageConvertS3Backend(t, fsVersion)
	}
}

func testImageConvertNoBackend(t *testing.T, fsVersion string) {
	testImageConvertBasic(&ConvertTestOption{
		t:         t,
		fsVersion: fsVersion,
	})
}

func testImageConvertS3Backend(t *testing.T, fsVersion string) {
	testOpt := &ConvertTestOption{
		t:         t,
		fsVersion: fsVersion,
	}
	rawConfig := []byte(`{
		"endpoint": "localhost:9000",
		"scheme": "http",
		"bucket_name": "nydus",
		"region": "us-east-1",
		"object_prefix": "path/to/my-registry/",
		"access_key_id": "minio",
		"access_key_secret": "minio123"
	}`)
	backend, err := backend.NewBackend("s3", rawConfig, true)
	if err != nil {
		t.Fatalf("failed to create s3 backend: %v", err)
	}
	testOpt.backend = backend

	minioContainerName := fmt.Sprintf("minio-%d", time.Now().UnixNano())
	testOpt.beforeConversionHook = func() error {
		// setup minio server
		if err := exec.Command("docker", "run", "-d", "-p", "9000:9000", "--name", minioContainerName, "-e", "MINIO_ACCESS_KEY=minio", "-e", "MINIO_SECRET_KEY=minio123", "minio/minio", "server", "/data").Run(); err != nil {
			t.Fatalf("failed to start minio server: %v", err)
			return err
		}
		time.Sleep(5 * time.Second)
		// create nydus bucket
		s3AWSConfig, err := awscfg.LoadDefaultConfig(context.TODO())
		if err != nil {
			t.Errorf("failed to load aws config")
		}
		client := s3.NewFromConfig(s3AWSConfig, func(o *s3.Options) {
			o.EndpointResolver = s3.EndpointResolverFromURL("http://localhost:9000")
			o.Region = "us-east-1"
			o.UsePathStyle = true
			o.Credentials = credentials.NewStaticCredentialsProvider("minio", "minio123", "")
			o.UsePathStyle = true
		})
		_, err = client.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String("nydus")})
		if err != nil {
			return err
		}
		logrus.Info("create s3 bucket successfully")
		return nil
	}

	testOpt.afterConversionHook = func() error {
		if err := exec.Command("docker", "rm", "-f", minioContainerName).Run(); err != nil {
			return err
		}
		return nil
	}

	// TODO by now, the last release of nydusify doesn't support s3 backend
	// so skip the check
	testOpt.disableCheck = true

	testImageConvertBasic(testOpt)
}

func testImageConvertBasic(testOpt *ConvertTestOption) {
	const (
		srcImageRef    = "docker.io/library/nginx:latest"
		targetImageRef = "localhost:5000/nydus/nginx:nydus-latest"
	)
	t := testOpt.t
	// setup docker registry
	if err := exec.Command("docker", "run", "-d", "-p", "5000:5000", "--restart=always", "--name", "registry", "registry:2").Run(); err != nil {
		t.Fatalf("failed to start docker registry: %v", err)
		return
	}
	defer func() {
		if err := exec.Command("docker", "stop", "registry").Run(); err != nil {
			t.Fatalf("failed to stop docker registry: %v", err)
		}
		if err := exec.Command("docker", "rm", "registry").Run(); err != nil {
			t.Fatalf("failed to remove docker registry: %v", err)
		}
	}()

	if testOpt.beforeConversionHook != nil {
		if err := testOpt.beforeConversionHook(); err != nil {
			t.Fatalf("failed to run before conversion hook: %v", err)
			return
		}
		defer func() {
			if err := testOpt.afterConversionHook(); err != nil {
				t.Fatalf("failed to run after conversion hook: %v", err)
			}
		}()
	}

	if err := exec.Command("ctr", "images", "pull", srcImageRef).Run(); err != nil {
		t.Fatalf("failed to pull image %s: %v", srcImageRef, err)
		return
	}
	defer func() {
		if err := exec.Command("ctr", "images", "rm", srcImageRef).Run(); err != nil {
			t.Fatalf("failed to remove image %s: %v", srcImageRef, err)
		}
	}()
	workDir, err := os.MkdirTemp("", fmt.Sprintf("nydus-containerd-converter-test-%d", time.Now().UnixNano()))
	require.NoError(t, err)
	defer os.RemoveAll(workDir)
	nydusOpts := &converter.PackOption{
		WorkDir:   workDir,
		FsVersion: testOpt.fsVersion,
		Backend:   testOpt.backend,
	}
	convertFunc := converter.LayerConvertFunc(*nydusOpts)
	convertHooks := containerdconverter.ConvertHooks{
		PostConvertHook: converter.ConvertHookFunc(converter.MergeOption{
			WorkDir:          nydusOpts.WorkDir,
			BuilderPath:      nydusOpts.BuilderPath,
			FsVersion:        nydusOpts.FsVersion,
			ChunkDictPath:    nydusOpts.ChunkDictPath,
			Backend:          testOpt.backend,
			PrefetchPatterns: nydusOpts.PrefetchPatterns,
		}),
	}
	convertFuncOpt := containerdconverter.WithIndexConvertFunc(
		containerdconverter.IndexConvertFuncWithHook(
			convertFunc,
			true,
			platforms.DefaultStrict(),
			convertHooks,
		),
	)
	client, err := containerd.New("/run/containerd/containerd.sock")
	if err != nil {
		t.Fatal(err)
		return
	}
	ctx := namespaces.WithNamespace(context.Background(), "default")
	if _, err = containerdconverter.Convert(ctx, client, targetImageRef, srcImageRef, convertFuncOpt); err != nil {
		t.Fatal(err)
		return
	}
	defer func() {
		if err := exec.Command("ctr", "images", "rm", targetImageRef).Run(); err != nil {
			t.Fatalf("failed to remove image %s: %v", targetImageRef, err)
		}
	}()
	// push target image
	if err := exec.Command("ctr", "images", "push", targetImageRef, "--plain-http").Run(); err != nil {
		t.Fatalf("failed to push image %s: %v", targetImageRef, err)
		return
	}
	// check whether the converted image is valid
	if testOpt.disableCheck {
		return
	}
	if output, err := exec.Command("nydusify", "check", "--source", srcImageRef, "--target", targetImageRef, "--target-insecure").CombinedOutput(); err != nil {
		t.Fatalf("failed to check image %s: %v, \noutput:\n%s", targetImageRef, err, output)
		return
	}
}
