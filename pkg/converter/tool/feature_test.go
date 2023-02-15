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
