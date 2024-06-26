/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package logging

import (
	"context"
	"os"
	"path/filepath"

	"github.com/containerd/log"
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

func SetUp(logLevel string, logToStdout bool, logDir string, logRotateArgs *RotateLogArgs) error {
	lvl, err := logrus.ParseLevel(logLevel)
	if err != nil {
		return err
	}
	logrus.SetLevel(lvl)

	if logToStdout {
		logrus.SetOutput(os.Stdout)
	} else {
		if logRotateArgs == nil {
			return errors.New("logRotateArgs is needed when logToStdout is false")
		}

		if err := os.MkdirAll(logDir, 0755); err != nil {
			return errors.Wrapf(err, "create log dir %s", logDir)
		}
		logFile := filepath.Join(logDir, defaultLogFileName)

		lumberjackLogger := &lumberjack.Logger{
			Filename:   logFile,
			MaxSize:    logRotateArgs.RotateLogMaxSize,
			MaxBackups: logRotateArgs.RotateLogMaxBackups,
			MaxAge:     logRotateArgs.RotateLogMaxAge,
			Compress:   logRotateArgs.RotateLogCompress,
			LocalTime:  logRotateArgs.RotateLogLocalTime,
		}
		logrus.SetOutput(lumberjackLogger)
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
