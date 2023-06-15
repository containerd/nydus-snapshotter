/*
* Copyright (c) 2023. Nydus Developers. All rights reserved.
*
* SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"log/syslog"
	"os"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"github.com/containerd/nydus-snapshotter/version"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
)

const (
	endpointPL      = "/api/v1/daemons/prefetch" //////////todo
	defaultEvents   = "RunPodSandbox"
	defaulthttp     = "http://system.sock"
	defaultsockaddr = "/run/containerd-nydus/system.sock"
)

type PluginArgs struct {
	PluginName   string
	PluginIdx    string
	PluginEvents string
	Sockaddr     string
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
			Name:        "sockaddr",
			Value:       defaultsockaddr,
			Usage:       "default unix domain socket address",
			Destination: &args.Sockaddr,
		},
		&cli.StringFlag{
			Name:        "events",
			Value:       defaultEvents,
			Usage:       "the events that containerd subscribes to. DO NOT CHANGE THIS.",
			Destination: &args.PluginEvents,
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
	globalsock string
	log        *logrus.Logger
	logWriter  *syslog.Writer
)

func sendDataOverHTTP(data string, endpoint string, sock string) error {
	url := defaulthttp + endpoint
	req, err := http.NewRequest("POST",
		url, bytes.NewBufferString(data))
	if err != nil {
		return err
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		return err
	}
	defer conn.Close()
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return conn, nil
			},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (p *plugin) RunPodSandbox(pod *api.PodSandbox) error {
	name := pod.Name
	parts := strings.Split(name, "-")
	name = parts[0]
	if pod.Annotations == nil {
		log.Printf("error: Pod annotations is nil")
		return errors.New("Pod annotations is nil")
	}

	prefetchList, ok := pod.Annotations["prefetch_list"]
	if !ok {
		errMsg := "Pod.yaml annotations don't have prefetch list."
		log.Printf("error: %s", errMsg)
		return errors.New(errMsg)
	}

	msg := fmt.Sprintf("%s : %s", name, prefetchList)
	err := sendDataOverHTTP(msg, endpointPL, globalsock)
	if err != nil {
		log.Printf("Failed to send data: %v\n", err)
		return err
	}

	return nil
}

func (p *plugin) onClose() {
	os.Exit(0)
}

func main() {

	flags := NewPluginFlags()
	app := &cli.App{
		Name:        "prefetchfiles-nri-plugin",
		Usage:       "NRI plugin for obtaining and transmitting prefetch files path",
		Version:     version.Version,
		Flags:       flags.F,
		HideVersion: true,
		Action: func(c *cli.Context) error {
			var (
				opts []stub.Option
				err  error
			)

			flags.Args.Sockaddr = c.String("sockaddr")
			globalsock = flags.Args.Sockaddr

			log = logrus.StandardLogger()
			log.SetFormatter(&logrus.TextFormatter{
				PadLevelText: true,
			})
			logWriter, err = syslog.New(syslog.LOG_INFO, "prefetchfiles-nri-plugin")

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
			log.Info("prefetchfiles NRI plugin exited")
		} else {
			log.WithError(err).Fatal("failed to start prefetchfiles NRI plugin")
		}
	}
}
