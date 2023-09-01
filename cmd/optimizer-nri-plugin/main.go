/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"context"
	"io"
	"log/syslog"
	"os"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/optimizer"
	"github.com/containerd/nydus-snapshotter/version"
	"github.com/pelletier/go-toml"
)

const (
	defaultEvents     = "StartContainer,StopContainer"
	defaultServerPath = "/usr/local/bin/optimizer-server"
	defaultPersistDir = "/opt/nri/optimizer/results"
	defaultServerType = optimizer.FANOTIFY
)

type PluginConfig struct {
	Events       []string
	optimizerCfg optimizer.Config
}

type PluginArgs struct {
	PluginName string
	PluginIdx  string
	Config     PluginConfig
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
			Name:        "server-type",
			Value:       defaultServerType,
			Usage:       "the type of optimizer, available value includes [\"ebpf\", \"fanotify\"]",
			Destination: &args.Config.optimizerCfg.ServerType,
		},
		&cli.StringFlag{
			Name:        "server-path",
			Value:       defaultServerPath,
			Usage:       "the path of optimizer server binary",
			Destination: &args.Config.optimizerCfg.ServerPath,
		},
		&cli.StringFlag{
			Name:        "persist-dir",
			Value:       defaultPersistDir,
			Usage:       "the directory to persist accessed files list for container",
			Destination: &args.Config.optimizerCfg.PersistDir,
		},
		&cli.BoolFlag{
			Name:        "readable",
			Value:       false,
			Usage:       "whether to make the csv file human readable",
			Destination: &args.Config.optimizerCfg.Readable,
		},
		&cli.IntFlag{
			Name:        "timeout",
			Value:       0,
			Usage:       "the timeout to kill optimizer server, 0 to disable it",
			Destination: &args.Config.optimizerCfg.Timeout,
		},
		&cli.BoolFlag{
			Name:        "overwrite",
			Usage:       "whether to overwrite the existed persistent files",
			Destination: &args.Config.optimizerCfg.Overwrite,
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
	cfg          PluginConfig
	log          *logrus.Logger
	logWriter    *syslog.Writer
	_            = stub.ConfigureInterface(&plugin{})
	globalServer = make(map[string]optimizer.Server)
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
	if err := tree.Unmarshal(&cfg.optimizerCfg); err != nil {
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
	imageName, server, err := optimizer.NewServer(cfg.optimizerCfg, container, logWriter)
	if err != nil {
		return err
	}

	if server == nil {
		return nil
	}

	if err := server.Start(); err != nil {
		return err
	}

	globalServer[imageName] = server

	return nil
}

func (p *plugin) StopContainer(_ *api.PodSandbox, container *api.Container) ([]*api.ContainerUpdate, error) {
	var update = []*api.ContainerUpdate{}
	_, imageName, err := optimizer.GetImageName(container.Annotations)
	if err != nil {
		return update, err
	}
	if server, ok := globalServer[imageName]; ok {
		server.Stop()
	}

	return update, nil
}

func (p *plugin) onClose() {
	for _, server := range globalServer {
		server.Stop()
	}
}

func main() {
	flags := NewPluginFlags()
	app := &cli.App{
		Name:        "optimizer-nri-plugin",
		Usage:       "Optimizer client for NRI plugin to manage optimizer server",
		Version:     version.Version,
		Flags:       flags.F,
		HideVersion: true,
		Action: func(c *cli.Context) error {
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

			if p.mask, err = api.ParseEventMask(defaultEvents); err != nil {
				log.Fatalf("failed to parse events: %v", err)
			}
			cfg.Events = strings.Split(defaultEvents, ",")

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
