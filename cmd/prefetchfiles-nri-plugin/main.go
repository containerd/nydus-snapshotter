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
	"path/filepath"
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
	defaultPrefetchConfigDir       = "/etc/nydus"
	nydusPrefetchAnnotation        = "containerd.io/nydus-prefetch"
)

type PluginArgs struct {
	PluginName    string
	PluginIdx     string
	SocketAddress string
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
			Usage:       "unix domain socket address. If defined in the configuration file, there is no need to add ",
			Destination: &args.SocketAddress,
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

			configFileName := "prefetchConfig.toml"
			configDir := defaultPrefetchConfigDir
			configFilePath := filepath.Join(configDir, configFileName)

			config, err := toml.LoadFile(configFilePath)
			if err != nil {
				log.Warnf("failed to read config file: %v", err)
			}

			configSocketAddrRaw := config.Get("file_prefetch.socket_address")
			if configSocketAddrRaw != nil {
				if configSocketAddr, ok := configSocketAddrRaw.(string); ok {
					globalSocket = configSocketAddr
				} else {
					log.Warnf("failed to read config: 'file_prefetch.socket_address' is not a string")
				}
			} else {
				globalSocket = flags.Args.SocketAddress
			}

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
