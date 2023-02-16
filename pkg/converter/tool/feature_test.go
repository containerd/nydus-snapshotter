/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tool

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFeature(t *testing.T) {
	features := Features{FeatureTar2Rafs}
	require.True(t, features.Contains(FeatureTar2Rafs))

	features.Remove(FeatureTar2Rafs)
	require.False(t, features.Contains(FeatureTar2Rafs))
}

func TestVersion(t *testing.T) {
	require.Equal(t, "0.1.0", detectVersion([]byte(`
	Version: 	0.1.0
	Git Commit: 	57a5ae40e91f82eb9d1e9934dee98358bcf822eb
	Build Time: 	Fri, 19 Mar 2021 10:45:00 +0000
	Profile: 	release
	Rustc: 		rustc 1.49.0 (e1884a8e3 2020-12-29)
	`)))

	require.Equal(t, "2.1.3", detectVersion([]byte(`
	Version: 	v2.1.3-rc1
	Git Commit: 	24c3bb9ab213ab94dfbf9ba4106042b34034a390
	Build Time: 	2023-01-19T02:26:07.782135583Z
	Profile: 	release
	Rustc: 		rustc 1.61.0 (fe5b13d68 2022-05-18)
	`)))

	require.Equal(t, "", detectVersion([]byte(`
	Version: 	unknown
	Git Commit: 	96efc2cf7e75174b49942fd41b84d672f921f9b4
	Build Time: 	2023-02-16T13:20:59.102548977Z
	Profile: 	release
	Rustc: 		rustc 1.66.1 (90743e729 2023-01-10)
	`)))
}
