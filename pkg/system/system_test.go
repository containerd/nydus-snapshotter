/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package system

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildUpgradeSocket(t *testing.T) {
	cur := "api.sock"

	next, err := buildNextAPISocket(cur)
	assert.Nil(t, err)
	assert.Equal(t, next, "api1.sock")

	cur = "api2.sock"

	next, err = buildNextAPISocket(cur)
	assert.Nil(t, err)
	assert.Equal(t, "api3.sock", next)

	cur = "api23.sock"

	next, err = buildNextAPISocket(cur)
	assert.Nil(t, err)
	assert.Equal(t, "api24.sock", next)

	cur = "api222.sock"

	next, err = buildNextAPISocket(cur)
	assert.Nil(t, err)
	assert.Equal(t, "api223.sock", next)
}

func TestJSONResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	jsonResponse(rec, map[string]string{"key": "value"})

	// Result() reports the headers as of WriteHeader, i.e. what is actually sent.
	resp := rec.Result()
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	assert.JSONEq(t, `{"key":"value"}`, rec.Body.String())
}
