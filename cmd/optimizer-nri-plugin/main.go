/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"context"
	"fmt"
	"io"
	"log/syslog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"

	"github.com/containerd/containerd/reference/docker"
	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/fanotify"
	"github.com/containerd/nydus-snapshotter/version"
	"github.com/pelletier/go-toml"
)

const (
	defaultEvents     = "StartContainer,StopContainer"
	defaultServerPath = "/usr/local/bin/optimizer-server"
	defaultPersistDir = "/opt/nri/optimizer/results"
)

type PluginConfig struct {
	Events []string `toml:"events"`

	ServerPath string `toml:"server_path"`
	PersistDir string `toml:"persist_dir"`
	Readable   bool   `toml:"readable"`
	Timeout    int    `toml:"timeout"`
	Overwrite  bool   `toml:"overwrite"`
}

type PluginArgs struct {
	PluginName   string
	PluginIdx    string
	PluginEvents string
	Config       PluginConfig
}

type Flags struct {
	Args *PluginArgs
	F    []cli.Flag
}

func buildFlags(args *PluginArgs) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "name",
			Usage:       "plugin name to register to NRI",
			Destination: &args.PluginName,
		},
		&cli.StringFlag{
			Name:        "idx",
			Usage:       "plugin index to register to NRI",
			Destination: &args.PluginIdx,
		},
		&cli.StringFlag{
			Name:        "events",
			Value:       defaultEvents,
			Usage:       "the events that containerd subscribes to. DO NOT CHANGE THIS.",
			Destination: &args.PluginEvents,
		},
		&cli.StringFlag{
			Name:        "server-path",
			Value:       defaultServerPath,
			Usage:       "the path of optimizer server binary",
			Destination: &args.Config.ServerPath,
		},
		&cli.StringFlag{
			Name:        "persist-dir",
			Value:       defaultPersistDir,
			Usage:       "the directory to persist accessed files list for container",
			Destination: &args.Config.PersistDir,
		},
		&cli.BoolFlag{
			Name:        "readable",
			Value:       false,
			Usage:       "whether to make the csv file human readable",
			Destination: &args.Config.Readable,
		},
		&cli.IntFlag{
			Name:        "timeout",
			Value:       0,
			Usage:       "the timeout to kill optimizer server, 0 to disable it",
			Destination: &args.Config.Timeout,
		},
		&cli.BoolFlag{
			Name:        "overwrite",
			Usage:       "whether to overwrite the existed persistent files",
			Destination: &args.Config.Overwrite,
		},
	}
}

func NewPluginFlags() *Flags {
	var args PluginArgs
	return &Flags{
		Args: &args,
		F:    buildFlags(&args),
	}
}

type plugin struct {
	stub stub.Stub
	mask stub.EventMask
}

var (
	cfg                  PluginConfig
	log                  *logrus.Logger
	logWriter            *syslog.Writer
	_                    = stub.ConfigureInterface(&plugin{})
	globalFanotifyServer = make(map[string]*fanotify.Server)
)

const (
	imageNameLabel = "io.kubernetes.cri.image-name"
)

func (p *plugin) Configure(config, runtime, version string) (stub.EventMask, error) {
	log.Infof("got configuration data: %q from runtime %s %s", config, runtime, version)
	if config == "" {
		return p.mask, nil
	}

	tree, err := toml.Load(config)
	if err != nil {
		return 0, errors.Wrap(err, "parse TOML")
	}
	if err := tree.Unmarshal(&cfg); err != nil {
		return 0, err
	}

	p.mask, err = api.ParseEventMask(cfg.Events...)
	if err != nil {
		return 0, errors.Wrap(err, "parse events in configuration")
	}

	log.Infof("configuration: %#v", cfg)

	return p.mask, nil
}

func (p *plugin) StartContainer(_ *api.PodSandbox, container *api.Container) error {
	dir, imageName, err := GetImageName(container.Annotations)
	if err != nil {
		return err
	}

	persistDir := filepath.Join(cfg.PersistDir, dir)
	if err := os.MkdirAll(persistDir, os.ModePerm); err != nil {
		return err
	}

	persistFile := filepath.Join(persistDir, imageName)
	if cfg.Timeout > 0 {
		persistFile = fmt.Sprintf("%s.timeout%ds", persistFile, cfg.Timeout)
	}

	fanotifyServer := fanotify.NewServer(cfg.ServerPath, container.Pid, imageName, persistFile, cfg.Readable, cfg.Overwrite, time.Duration(cfg.Timeout)*time.Second, logWriter)

	if err := fanotifyServer.RunServer(); err != nil {
		return err
	}

	globalFanotifyServer[imageName] = fanotifyServer

	return nil
}

func (p *plugin) StopContainer(_ *api.PodSandbox, container *api.Container) ([]*api.ContainerUpdate, error) {
	var update = []*api.ContainerUpdate{}
	_, imageName, err := GetImageName(container.Annotations)
	if err != nil {
		return update, err
	}
	if fanotifyServer, ok := globalFanotifyServer[imageName]; ok {
		fanotifyServer.StopServer()
	} else {
		return nil, errors.New("can not find fanotify server for container image " + imageName)
	}

	return update, nil
}

func GetImageName(annotations map[string]string) (string, string, error) {
	named, err := docker.ParseDockerRef(annotations[imageNameLabel])
	if err != nil {
		return "", "", err
	}
	nameTagged := named.(docker.NamedTagged)
	repo := docker.Path(nameTagged)

	dir := filepath.Dir(repo)
	image := filepath.Base(repo)

	imageName := image + ":" + nameTagged.Tag()

	return dir, imageName, nil
}

func (p *plugin) onClose() {
	for _, fanotifyServer := range globalFanotifyServer {
		fanotifyServer.StopServer()
	}
	os.Exit(0)
}

func main() {
	flags := NewPluginFlags()
	app := &cli.App{
		Name:        "optimizer-nri-plugin",
		Usage:       "Optimizer client for NRI plugin to manage optimizer server",
		Version:     version.Version,
		Flags:       flags.F,
		HideVersion: true,
		Action: func(_ *cli.Context) error {
			var (
				opts []stub.Option
				err  error
			)

			cfg = flags.Args.Config

			log = logrus.StandardLogger()
			log.SetFormatter(&logrus.TextFormatter{
				PadLevelText: true,
			})
			logWriter, err = syslog.New(syslog.LOG_INFO, "optimizer-nri-plugin")
			if err == nil {
				log.SetOutput(io.MultiWriter(os.Stdout, logWriter))
			}

			if flags.Args.PluginName != "" {
				opts = append(opts, stub.WithPluginName(flags.Args.PluginName))
			}
			if flags.Args.PluginIdx != "" {
				opts = append(opts, stub.WithPluginIdx(flags.Args.PluginIdx))
			}

			p := &plugin{}

			if p.mask, err = api.ParseEventMask(flags.Args.PluginEvents); err != nil {
				log.Fatalf("failed to parse events: %v", err)
			}
			cfg.Events = strings.Split(flags.Args.PluginEvents, ",")

			if p.stub, err = stub.New(p, append(opts, stub.WithOnClose(p.onClose))...); err != nil {
				log.Fatalf("failed to create plugin stub: %v", err)
			}

			err = p.stub.Run(context.Background())
			if err != nil {
				log.Errorf("plugin exited with error %v", err)
				os.Exit(1)
			}

			return nil
		},
	}
	if err := app.Run(os.Args); err != nil {
		if errdefs.IsConnectionClosed(err) {
			log.Info("optimizer NRI plugin exited")
		} else {
			log.WithError(err).Fatal("failed to start optimizer NRI plugin")
		}
	}
}
