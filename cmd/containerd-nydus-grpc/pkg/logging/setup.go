/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package logging

import (
	"context"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	DefaultLogDirName  = "logs"
	defaultLogFileName = "nydus-snapshotter.log"
)

func SetUp(logLevel string, logToStdout bool, logDir string, rootDir string) error {
	lvl, err := logrus.ParseLevel(logLevel)
	if err != nil {
		return err
	}
	logrus.SetLevel(lvl)

	if logToStdout {
		logrus.SetOutput(os.Stdout)
	} else {
		if len(logDir) == 0 {
			logDir = filepath.Join(rootDir, DefaultLogDirName)
		}
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return errors.Wrapf(err, "create log dir %s", logDir)
		}
		logFile := filepath.Join(logDir, defaultLogFileName)
		f, err := os.OpenFile(logFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0755)
		if err != nil {
			return errors.Wrapf(err, "open log file %s", logFile)
		}
		logrus.SetOutput(f)
	}

	logrus.SetFormatter(&logrus.TextFormatter{
		TimestampFormat: log.RFC3339NanoFixed,
		FullTimestamp:   true,
	})
	return nil
}

func WithContext() context.Context {
	return log.WithLogger(context.Background(), log.L)
}
