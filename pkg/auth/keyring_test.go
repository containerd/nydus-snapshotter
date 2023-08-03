/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"testing"

	"github.com/containerd/containerd/log"
	"github.com/stretchr/testify/assert"
	"golang.org/x/sys/unix"
)

func TestKeyRing_Add(t *testing.T) {
	A := assert.New(t)

	err := ClearKeyring()
	A.NoError(err)

	testKey := "test"
	testValue := "value"
	keyID, err := AddKeyring(testKey, testValue)
	if err != nil && err == unix.EINVAL {
		return
	}
	A.NoError(err)

	log.L.Infof("[abin] keyID: %d", keyID)
	value, err := getData(keyID)
	if err != nil && err == unix.EINVAL {
		return
	}
	A.NoError(err)
	A.Equal(testValue, value)

	value, err = getData(0)
	A.ErrorContains(err, "required key not available")
	A.Equal("", value)
}

func TestKeyRing_Search(t *testing.T) {
	A := assert.New(t)

	err := ClearKeyring()
	A.NoError(err)

	testKey := "test"
	testValue := "value"
	_, err = AddKeyring(testKey, testValue)
	if err != nil && err == unix.EINVAL {
		return
	}
	A.NoError(err)

	value, err := SearchKeyring(testKey)
	A.NoError(err)
	A.Equal(testValue, value)

	value, err = SearchKeyring("invalidKey")
	A.ErrorContains(err, "required key not available")
	A.Equal("", value)
}

func TestKeyRing_getData(t *testing.T) {
	A := assert.New(t)

	err := ClearKeyring()
	A.NoError(err)

	testKey := "test"
	tests := []struct {
		name   string
		length int
	}{
		{
			name:   "data length 64 bytes",
			length: 64,
		},
		{
			name:   "data length 512 bytes",
			length: 512,
		},
		{
			name:   "data length 513 bytes",
			length: 513,
		},
		{
			name:   "data length 1024 bytes",
			length: 1024,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var testValue []byte
			for i := 0; i < tt.length; i++ {
				testValue = append(testValue, 'A')
			}

			keyID, err := AddKeyring(testKey, string(testValue))
			if err != nil && err == unix.EINVAL {
				return
			}
			A.NoError(err)

			value, err := getData(keyID)
			if err != nil && err == unix.EINVAL {
				return
			}
			A.NoError(err)
			A.Equal(tt.length, len([]byte(value)))
		})
	}
}
