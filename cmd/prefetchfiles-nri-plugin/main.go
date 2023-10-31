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
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"github.com/pelletier/go-toml"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"

	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/version"
)

const (
	endpointPrefetch               = "/api/v1/prefetch"
	defaultEvents                  = "RunPodSandbox"
	defaultSystemControllerAddress = "/run/containerd-nydus/system.sock"
	nydusPrefetchAnnotation        = "containerd.io/nydus-prefetch"
)

type PluginConfig struct {
	SocketAddr string `toml:"socket_address"`
}

type PluginArgs struct {
	PluginName string
	PluginIdx  string
	Config     PluginConfig
}

type Flags struct {
	Args *PluginArgs
	Flag []cli.Flag
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
			Name:        "socket-addr",
			Value:       defaultSystemControllerAddress,
			Usage:       "UNIX domain socket address for connection to the nydus-snapshotter API.",
			Destination: &args.Config.SocketAddr,
		},
	}
}

func NewPluginFlags() *Flags {
	var args PluginArgs
	return &Flags{
		Args: &args,
		Flag: buildFlags(&args),
	}
}

type plugin struct {
	stub stub.Stub
	mask stub.EventMask
}

var (
	globalSocket string
	log          *logrus.Logger
	logWriter    *syslog.Writer
)

// sendDataOverHTTP sends the prefetch data to the specified endpoint over HTTP using a Unix socket.
func sendDataOverHTTP(data string, endpoint, sock string) error {
	url := fmt.Sprintf("http://unix%s", endpoint)

	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(data))
	if err != nil {
		return err
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sock)
			},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to send data, status code: %d", resp.StatusCode)
	}
	resp.Body.Close()

	return nil
}

func (p *plugin) RunPodSandbox(pod *api.PodSandbox) error {
	prefetchList, ok := pod.Annotations[nydusPrefetchAnnotation]
	if !ok {
		return nil
	}

	err := sendDataOverHTTP(prefetchList, endpointPrefetch, globalSocket)
	if err != nil {
		log.Errorf("failed to send data: %v", err)
		return err
	}

	return nil
}
func (p *plugin) Configure(config, runtime, version string) (stub.EventMask, error) {
	var cfg PluginConfig
	log.Infof("got configuration data: %q from runtime %s %s", config, runtime, version)
	if config == "" {
		return p.mask, nil
	}

	tree, err := toml.Load(config)
	if err != nil {
		return 0, err
	}
	if err := tree.Unmarshal(&cfg); err != nil {
		return 0, err
	}
	p.mask, err = api.ParseEventMask(defaultEvents)
	if err != nil {
		return 0, errors.Wrap(err, "parse events in configuration")
	}

	log.Infof("configuration: %#v", cfg)
	globalSocket = cfg.SocketAddr

	return p.mask, nil
}

func main() {

	flags := NewPluginFlags()

	app := &cli.App{
		Name:        "prefetch-nri-plugin",
		Usage:       "NRI plugin for obtaining and transmitting prefetch files path",
		Version:     version.Version,
		Flags:       flags.Flag,
		HideVersion: true,
		Action: func(c *cli.Context) error {
			var (
				opts []stub.Option
				err  error
			)

			log = logrus.StandardLogger()

			globalSocket = flags.Args.Config.SocketAddr

			log.SetFormatter(&logrus.TextFormatter{
				PadLevelText: true,
			})
			logWriter, err = syslog.New(syslog.LOG_INFO, "prefetch-nri-plugin")

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

			if p.stub, err = stub.New(p, opts...); err != nil {
				log.Fatalf("failed to create plugin stub: %v", err)
			}

			err = p.stub.Run(context.Background())
			if err != nil {
				return errors.Wrap(err, "plugin exited")
			}
			return nil
		},
	}
	if err := app.Run(os.Args); err != nil {

		if errdefs.IsConnectionClosed(err) {
			log.Info("prefetch NRI plugin exited")
		} else {
			log.WithError(err).Fatal("failed to start prefetch NRI plugin")
		}
	}
}
