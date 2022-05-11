/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package converter

import (
	"github.com/containerd/containerd/content"
	"github.com/opencontainers/go-digest"
)

type Layer struct {
	// Digest represents the hash of whole tar blob.
	Digest digest.Digest
	// ReaderAt holds the reader of whole tar blob.
	ReaderAt content.ReaderAt
}

type ConvertOption struct {
	// RafsVersion specifies nydus format version, possible values:
	// `5`, `6` (EROFS-compatible), default is `5`.
	RafsVersion string
	// ChunkDictPath holds the bootstrap path of chunk dict image.
	ChunkDictPath string
	// PrefetchPatterns holds file path pattern list want to prefetch.
	PrefetchPatterns string
}

type MergeOption struct {
	// ChunkDictPath holds the bootstrap path of chunk dict image.
	ChunkDictPath string
	// PrefetchPatterns holds file path pattern list want to prefetch.
	PrefetchPatterns string
	// WithTar puts bootstrap into a tar stream (no gzip).
	WithTar bool
}
