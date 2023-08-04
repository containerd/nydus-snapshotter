/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCache_UpdateAuth(t *testing.T) {
	A := assert.New(t)

	cache := NewCache()

	testKey1 := "docker.io"
	testValue := "dXNlcjpwYXNzd29yZAo="
	err := cache.UpdateAuth(testKey1, testValue)
	A.NoError(err)

	value, err := cache.GetAuth(testKey1)
	A.NoError(err)
	A.Equal(testValue, value)

	keyChain, err := cache.GetKeyChain(testKey1)
	A.NoError(err)
	A.Equal(keyChain.ToBase64(), value)

	value, err = cache.GetAuth("invalidKey")
	A.ErrorContains(err, "required key not available")
	A.Equal(value, "")

	testInvalidAuthValue := "invalidAuthValue"
	err = cache.UpdateAuth(testKey1, testInvalidAuthValue)
	A.NoError(err)

	keyChain, err = cache.GetKeyChain(testKey1)
	A.ErrorContains(err, "invalid registry auth token")
	A.Nil(keyChain)
}
