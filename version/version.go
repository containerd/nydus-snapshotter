/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package version

import "runtime"

var (
	// Version holds the complete version number. Filled in at linking time.
	Version = "unknown"

	// Revision is filled with the VCS (e.g. git) revision being used to build
	// the program at linking time.
	Revision = "unknown"

	// GoVersion is Go tree's version.
	GoVersion = runtime.Version()

	// BuildTimestamp is timestamp of building.
	BuildTimestamp = "unknown"
)
