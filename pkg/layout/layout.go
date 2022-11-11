/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package layout

import (
	"encoding/binary"
	"errors"
	"unsafe"
)

// RafsV6 layout: 1k + SuperBlock(128) + SuperBlockExtended(256)
// RafsV5 layout: 8K superblock
// So we only need to read the MaxSuperBlockSize size to include both v5 and v6 superblocks
const MaxSuperBlockSize = 8 * 1024
const (
	RafsV5                 string = "v5"
	RafsV6                 string = "v6"
	RafsV5SuperVersion     uint32 = 0x500
	RafsV5SuperMagic       uint32 = 0x5241_4653
	RafsV6SuperMagic       uint32 = 0xE0F5_E1E2
	RafsV6SuperBlockSize   uint32 = 1024 + 128 + 256
	RafsV6SuperBlockOffset uint32 = 1024
	RafsV6ChunkInfoOffset  uint32 = 1024 + 128 + 24
	BootstrapFile          string = "image/image.boot"
	LegacyBootstrapFile    string = "image.boot"
	DummyMountpoint        string = "/dummy"
)

var nativeEndian binary.ByteOrder

type ImageMode int

const (
	OnDemand ImageMode = iota
	PreLoad
)

func init() {
	buf := [2]byte{}
	*(*uint16)(unsafe.Pointer(&buf[0])) = uint16(0xABCD)

	switch buf {
	case [2]byte{0xCD, 0xAB}:
		nativeEndian = binary.LittleEndian
	case [2]byte{0xAB, 0xCD}:
		nativeEndian = binary.BigEndian
	default:
		panic("Could not determine native endianness.")
	}
}

func isRafsV6(buf []byte) bool {
	return nativeEndian.Uint32(buf[RafsV6SuperBlockOffset:]) == RafsV6SuperMagic
}

func DetectFsVersion(header []byte) (string, error) {
	if len(header) < 8 {
		return "", errors.New("header buffer to DetectFsVersion is too small")
	}
	magic := binary.LittleEndian.Uint32(header[0:4])
	fsVersion := binary.LittleEndian.Uint32(header[4:8])
	if magic == RafsV5SuperMagic && fsVersion == RafsV5SuperVersion {
		return RafsV5, nil
	}

	// FIXME: detect more magic numbers to reduce collision
	if len(header) >= int(RafsV6SuperBlockSize) && isRafsV6(header) {
		return RafsV6, nil
	}

	return "", errors.New("unknown file system header")
}
