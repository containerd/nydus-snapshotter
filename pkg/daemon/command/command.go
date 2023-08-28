/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package command

import (
	"fmt"
	"reflect"
	"strconv"

	"github.com/pkg/errors"
)

type Opt = func(cmd *DaemonCommand)

// Define how to build a command line to start a nydusd daemon
type DaemonCommand struct {
	// "singleton" "fuse"
	Mode string `type:"subcommand"`
	// "blobcache" "fscache" "virtiofs"
	FscacheDriver  string `type:"param" name:"fscache"`
	FscacheThreads string `type:"param" name:"fscache-threads"`
	Upgrade        bool   `type:"flag" name:"upgrade" default:""`
	ThreadNum      string `type:"param" name:"thread-num"`
	// `--id` is required by `--supervisor` when starting nydusd
	ID              string `type:"param" name:"id"`
	Config          string `type:"param" name:"config"`
	Bootstrap       string `type:"param" name:"bootstrap"`
	Mountpoint      string `type:"param" name:"mountpoint"`
	APISock         string `type:"param" name:"apisock"`
	LogLevel        string `type:"param" name:"log-level"`
	LogRotationSize int    `type:"param" name:"log-rotation-size"`
	Supervisor      string `type:"param" name:"supervisor"`
	LogFile         string `type:"param" name:"log-file"`
	PrefetchFiles   string `type:"param" name:"prefetch-files"`
}

// Build exec style command line
func BuildCommand(opts []Opt) ([]string, error) {
	var cmd DaemonCommand
	var subcommand string

	for _, o := range opts {
		o(&cmd)
	}

	args := make([]string, 0, 32)
	t := reflect.TypeOf(cmd)
	v := reflect.ValueOf(cmd)

	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag
		argType := tag.Get("type")

		switch argType {
		case "param":
			// Zero value will be skipped appending to command line
			if v.Field(i).IsZero() {
				continue
			}

			value := v.Field(i).Interface()

			pair := []string{fmt.Sprintf("--%s", tag.Get("name")), fmt.Sprintf("%v", value)}
			args = append(args, pair...)
		case "subcommand":
			// Zero value will be skipped appending to command line
			if v.Field(i).IsZero() {
				continue
			}
			subcommand = v.Field(i).String()
		case "flag":
			kind := v.Field(i).Kind()

			if kind != reflect.Bool {
				return nil, errors.Errorf("flag must be boolean")
			}

			v := v.Field(i).Bool()

			if v {
				flag := fmt.Sprintf("--%s", tag.Get("name"))
				args = append(args, flag)
			} else {
				continue
			}
		default:
			return nil, errors.Errorf("unknown tag type: %s ", argType)
		}
	}

	if subcommand != "" {
		// Ensure subcommand is at the first place.
		args = append([]string{subcommand}, args...)
	}

	return args, nil
}

func WithMode(m string) Opt {
	return func(cmd *DaemonCommand) {
		cmd.Mode = m
	}
}

func WithPrefetchFiles(p string) Opt {
	return func(cmd *DaemonCommand) {
		cmd.PrefetchFiles = p
	}
}

func WithFscacheDriver(w string) Opt {
	return func(cmd *DaemonCommand) {
		cmd.FscacheDriver = w
	}
}

func WithFscacheThreads(num int) Opt {
	return func(cmd *DaemonCommand) {
		cmd.FscacheThreads = strconv.Itoa(num)
	}
}

func WithThreadNum(num int) Opt {
	return func(cmd *DaemonCommand) {
		cmd.ThreadNum = strconv.Itoa(num)
	}
}

func WithConfig(config string) Opt {
	return func(cmd *DaemonCommand) {
		cmd.Config = config
	}
}

func WithBootstrap(b string) Opt {
	return func(cmd *DaemonCommand) {
		cmd.Bootstrap = b
	}
}

func WithMountpoint(m string) Opt {
	return func(cmd *DaemonCommand) {
		cmd.Mountpoint = m
	}
}

func WithAPISock(api string) Opt {
	return func(cmd *DaemonCommand) {
		cmd.APISock = api
	}
}

func WithLogFile(l string) Opt {
	return func(cmd *DaemonCommand) {
		cmd.LogFile = l
	}
}

func WithLogLevel(l string) Opt {
	return func(cmd *DaemonCommand) {
		cmd.LogLevel = l
	}
}

func WithLogRotationSize(l int) Opt {
	return func(cmd *DaemonCommand) {
		cmd.LogRotationSize = l
	}
}

func WithSupervisor(s string) Opt {
	return func(cmd *DaemonCommand) {
		cmd.Supervisor = s
	}
}

func WithID(id string) Opt {
	return func(cmd *DaemonCommand) {
		cmd.ID = id
	}
}

func WithUpgrade() Opt {
	return func(cmd *DaemonCommand) {
		cmd.Upgrade = true
	}
}
