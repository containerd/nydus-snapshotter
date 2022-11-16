/*
 * Copyright (c) 2022. Ant Group. All rights reserved.
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
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

const (
	DefaultLogDirName  = "logs"
	defaultLogFileName = "nydus-snapshotter.log"
)

type RotateLogArgs struct {
	RotateLogMaxSize    int
	RotateLogMaxBackups int
	RotateLogMaxAge     int
	RotateLogLocalTime  bool
	RotateLogCompress   bool
}

func New(logLevel string, logToStdout bool, logDir string,
	rootDir string, logRotateArgs RotateLogArgs) error {
	lvl, err := logrus.ParseLevel(logLevel)
	if err != nil {
		return err
	}
	logrus.SetLevel(lvl)

	if logToStdout {
		logrus.SetOutput(os.Stdout)
	} else {
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return errors.Wrapf(err, "create log dir %s", logDir)
		}
		logFile := filepath.Join(logDir, defaultLogFileName)

		logrus.SetOutput(&lumberjack.Logger{
			Filename:   logFile,
			MaxSize:    logRotateArgs.RotateLogMaxSize,
			MaxBackups: logRotateArgs.RotateLogMaxBackups,
			MaxAge:     logRotateArgs.RotateLogMaxAge,
			Compress:   logRotateArgs.RotateLogCompress,
			LocalTime:  logRotateArgs.RotateLogLocalTime,
		})
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
