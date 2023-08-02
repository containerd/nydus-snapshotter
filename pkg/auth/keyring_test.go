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

func TestKeyRing_Add(t *testing.T) {
	A := assert.New(t)

	testKey := "test"
	testValue := "value"
	keyID, err := AddKeyring(testKey, testValue)
	A.NoError(err)

	value, err := getData(keyID)
	A.NoError(err)
	A.Equal(testValue, value)

	value, err = getData(-1)
	A.ErrorContains(err, "required key not available")
	A.Equal("", value)
}

func TestKeyRing_Search(t *testing.T) {
	A := assert.New(t)

	testKey := "test"
	testValue := "value"
	_, err := AddKeyring(testKey, testValue)
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

			keyID, err := AddKeyring(testKey, string(testValue[:]))
			A.NoError(err)

			value, err := getData(keyID)
			A.NoError(err)
			A.Equal(tt.length, len([]byte(value)))
		})
	}
}