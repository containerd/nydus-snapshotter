/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package config

const (
	// nydusdBinaryName is name of nydusd.
	nydusdBinaryName = "nydusd"

	// nydusImageBinaryName is image name of nydusd.
	nydusImageBinaryName = "nydus-image"
)

const (
	// Log rotation
	defaultRotateLogMaxSize    = 200 // 200 megabytes
	defaultRotateLogMaxBackups = 10
	defaultRotateLogMaxAge     = 0 // days
	defaultRotateLogLocalTime  = true
	defaultRotateLogCompress   = true
)
