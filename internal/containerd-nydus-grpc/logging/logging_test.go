/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/sirupsen/logrus"
	"gotest.tools/assert"
)

const (
	TestLogDirName  = "test-rotate-logs"
	TestRootDirName = "test-root"
)

func GetRotateLogFileNumbers(testLogDir string, suffix string) int {
	i := 0
	filepath.Walk(testLogDir, func(fname string, fi os.FileInfo, err error) error {
		if !fi.IsDir() && strings.HasSuffix(fname, suffix) {
			i++
		}
		return nil
	})
	return i
}

func TestSetUp(t *testing.T) {
	// Try to clean previously created test directory.
	os.RemoveAll(TestLogDirName)

	logRotateArgs := RotateLogArgs{
		RotateLogMaxSize:    1, // 1MB
		RotateLogMaxBackups: 5,
		RotateLogMaxAge:     0,
		RotateLogLocalTime:  true,
		RotateLogCompress:   true,
	}
	logLevel := logrus.InfoLevel.String()

	err := New(logLevel, true, TestLogDirName, TestRootDirName, RotateLogArgs{})
	assert.NilError(t, err, nil)

	err = New(logLevel, false, TestLogDirName, TestRootDirName, RotateLogArgs{})
	assert.ErrorContains(t, err, "logRotateArgs is needed when logToStdout is false")

	err = New(logLevel, false, TestLogDirName, TestRootDirName, logRotateArgs)
	assert.NilError(t, err)
	for i := 0; i < 100000; i++ { // total 9.1MB
		log.L.Infof("test log, now: %s", time.Now().Format("2006-01-02 15:04:05"))
	}
	assert.Equal(t, GetRotateLogFileNumbers(TestLogDirName, "log.gz"), logRotateArgs.RotateLogMaxBackups)

	os.RemoveAll(TestLogDirName)
}
