/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tests

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/nydus-snapshotter/pkg/converter"
)

const envNydusdPath = "NYDUS_NYDUSD"

var fsVersion = flag.String("fs-version", "5", "specifie the fs version for test")

func hugeString(mb int) string {
	var buf strings.Builder
	size := mb * 1024 * 1024
	seqSize := 512 * 1024
	buf.Grow(size)

	seq := size / seqSize

	for i := 0; i < seq; i++ {
		data := make([]byte, seqSize)
		if i%2 == 0 {
			rand.Read(data)
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
	hdr := &tar.Header{
		Name: name,
		Mode: 0444,
		Size: int64(len(data)),
	}
	err := tw.WriteHeader(hdr)
	require.NoError(t, err)

	io.Copy(tw, bytes.NewReader([]byte(data)))
	require.NoError(t, err)
}

func writeDirToTar(t *testing.T, tw *tar.Writer, name string) {
	hdr := &tar.Header{
		Name:     name,
		Mode:     0444,
		Typeflag: tar.TypeDir,
	}
	err := tw.WriteHeader(hdr)
	require.NoError(t, err)
}

func writeToFile(t *testing.T, reader io.Reader, fileName string) {
	file, err := os.Create(fileName)
	require.NoError(t, err)
	defer file.Close()

	_, err = io.Copy(file, reader)
	require.NoError(t, err)
}

var expectedFileTree = map[string]string{
	"dir-1":        "",
	"dir-1/file-2": "lower-file-2",
	"dir-2":        "",
	"dir-2/file-1": hugeString(3),
	"dir-2/file-2": "upper-file-2",
	"dir-2/file-3": "upper-file-3",
}

func buildChunkDictTar(t *testing.T) io.ReadCloser {
	pr, pw := io.Pipe()
	tw := tar.NewWriter(pw)

	go func() {
		defer pw.Close()

		writeDirToTar(t, tw, "dir-1")
		writeFileToTar(t, tw, "dir-1/file-1", "lower-file-1")
		writeFileToTar(t, tw, "dir-1/file-2", "lower-file-2")
		writeFileToTar(t, tw, "dir-1/file-3", "lower-file-3")

		require.NoError(t, tw.Close())
	}()

	return pr
}

func buildOCILowerTar(t *testing.T) io.ReadCloser {
	pr, pw := io.Pipe()
	tw := tar.NewWriter(pw)

	go func() {
		defer pw.Close()

		writeDirToTar(t, tw, "dir-1")
		writeFileToTar(t, tw, "dir-1/file-1", "lower-file-1")
		writeFileToTar(t, tw, "dir-1/file-2", "lower-file-2")

		writeDirToTar(t, tw, "dir-2")
		writeFileToTar(t, tw, "dir-2/file-1", "lower-file-1")

		require.NoError(t, tw.Close())
	}()

	return pr
}

func buildOCIUpperTar(t *testing.T, teePath string) io.ReadCloser {
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
		writeFileToTar(t, tw, "dir-1/.wh.file-1", "")

		writeDirToTar(t, tw, "dir-2")
		writeFileToTar(t, tw, "dir-2/.wh..wh..opq", "")
		writeFileToTar(t, tw, "dir-2/file-1", expectedFileTree["dir-2/file-1"])
		writeFileToTar(t, tw, "dir-2/file-2", "upper-file-2")
		writeFileToTar(t, tw, "dir-2/file-3", "upper-file-3")

		require.NoError(t, tw.Close())
	}()

	return pr
}

func convertLayer(t *testing.T, source io.ReadCloser, chunkDict, workDir string, fsVersion string) (string, digest.Digest) {
	var data bytes.Buffer
	writer := bufio.NewWriter(&data)

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

func unpackLayer(t *testing.T, workDir string, ra content.ReaderAt) (string, digest.Digest) {
	var data bytes.Buffer
	writer := bufio.NewWriter(&data)

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

func verify(t *testing.T, workDir string) {
	mountDir := filepath.Join(workDir, "mnt")
	blobDir := filepath.Join(workDir, "blobs")
	nydusdPath := os.Getenv(envNydusdPath)
	if nydusdPath == "" {
		nydusdPath = "nydusd"
	}
	mode := "cached"
	digestValidate := true
	// Currently v6 does not support digestValidate, and only direct mode is supported
	if *fsVersion == "6" {
		mode = "direct"
		digestValidate = false
	}
	config := NydusdConfig{
		EnablePrefetch: true,
		NydusdPath:     nydusdPath,
		BootstrapPath:  filepath.Join(workDir, "bootstrap"),
		ConfigPath:     filepath.Join(workDir, "nydusd-config.fusedev.json"),
		BackendType:    "localfs",
		BackendConfig:  fmt.Sprintf(`{"dir": "%s"}`, blobDir),
		BlobCacheDir:   filepath.Join(workDir, "cache"),
		APISockPath:    filepath.Join(workDir, "nydusd-api.sock"),
		MountPath:      mountDir,
		Mode:           mode,
		DigestValidate: digestValidate,
	}

	nydusd, err := NewNydusd(config)
	require.NoError(t, err)
	err = nydusd.Mount()
	require.NoError(t, err)
	defer nydusd.Umount()

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
			_data, err := ioutil.ReadAll(file)
			require.NoError(t, err)
			data = string(_data)
		}
		actualFileTree[targetPath] = data

		return nil
	})
	require.NoError(t, err)

	require.Equal(t, expectedFileTree, actualFileTree)
}

func buildChunkDict(t *testing.T, workDir string) (string, string) {
	dictOCITarReader := buildChunkDictTar(t)

	blobDir := filepath.Join(workDir, "blobs")
	nydusTarPath, lowerNydusBlobDigest := convertLayer(t, dictOCITarReader, "", blobDir, *fsVersion)
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

	err = converter.Merge(context.TODO(), layers, file, converter.MergeOption{})
	require.NoError(t, err)

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

// sudo go test -v -count=1 -run TestConverter ./pkg/nydusify
func TestConverter(t *testing.T) {
	workDir, err := ioutil.TempDir("", "nydus-converter-test-")
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	lowerOCITarReader := buildOCILowerTar(t)
	upperOCITarReader := buildOCIUpperTar(t, "")

	blobDir := filepath.Join(workDir, "blobs")
	err = os.MkdirAll(blobDir, 0755)
	require.NoError(t, err)

	cacheDir := filepath.Join(workDir, "cache")
	err = os.MkdirAll(cacheDir, 0755)
	require.NoError(t, err)

	mountDir := filepath.Join(workDir, "mnt")
	err = os.MkdirAll(mountDir, 0755)
	require.NoError(t, err)

	chunkDictBootstrapPath, chunkDictBlobHash := buildChunkDict(t, workDir)

	lowerNydusTarPath, lowerNydusBlobDigest := convertLayer(t, lowerOCITarReader, chunkDictBootstrapPath, blobDir, *fsVersion)
	upperNydusTarPath, upperNydusBlobDigest := convertLayer(t, upperOCITarReader, chunkDictBootstrapPath, blobDir, *fsVersion)

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

	err = converter.Merge(context.TODO(), layers, file, converter.MergeOption{
		ChunkDictPath: chunkDictBootstrapPath,
	})
	require.NoError(t, err)

	verify(t, workDir)
	dropCache(t)
	verify(t, workDir)

	ensureFile(t, filepath.Join(cacheDir, chunkDictBlobHash)+".chunk_map")
	ensureNoFile(t, filepath.Join(cacheDir, lowerNydusBlobDigest.Hex())+".chunk_map")
	ensureFile(t, filepath.Join(cacheDir, upperNydusBlobDigest.Hex())+".chunk_map")
}

func TestUnpack(t *testing.T) {
	workDir, err := ioutil.TempDir("", "nydus-converter-test-")
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	ociTar := filepath.Join(workDir, "oci.tar")
	ociTarReader := buildOCIUpperTar(t, ociTar)
	nydusTar, _ := convertLayer(t, ociTarReader, "", workDir, *fsVersion)

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
