/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/syslog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
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
	defaultEvents       = "StartContainer,StopContainer"
	defaultServerPath   = "/usr/local/bin/optimizer-server"
	defaultPersistDir   = "/opt/nri/optimizer/results"
	socketAddr          = "/run/optimizer/prefetch.sock"
	defaultPostEndpoint = "/api/v1/prefetch/upload"
	defaultGetEndpoint  = "/api/v1/prefetch/download"
)

type PluginConfig struct {
	Events []string `toml:"events"`

	ServerPath              string `toml:"server_path"`
	PersistDir              string `toml:"persist_dir"`
	Readable                bool   `toml:"readable"`
	Timeout                 int    `toml:"timeout"`
	Overwrite               bool   `toml:"overwrite"`
	PrefetchDistributionURL string `toml:"prefetch_distribution_url"`
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
		&cli.StringFlag{
			Name:        "prefetch-distribution-url",
			Usage:       "The service url of prefetch distribution, for example: http://localhost:1323",
			Destination: &args.Config.PrefetchDistributionURL,
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
	cfg                      PluginConfig
	log                      *logrus.Logger
	logWriter                *syslog.Writer
	_                        = stub.ConfigureInterface(&plugin{})
	globalFanotifyServer     = make(map[string]*fanotify.Server)
	globalFanotifyServerLock sync.Mutex
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

type CacheItem struct {
	ImageName     string
	ContainerName string
	PrefetchFiles []string
}

type Cache struct {
	Items map[string]*CacheItem
}

func (p *plugin) StartContainer(_ *api.PodSandbox, container *api.Container) error {
	dir, imageName, imageRepo, err := GetImageName(container.Annotations)
	if err != nil {
		return err
	}
	containerName := container.Name

	persistDir := filepath.Join(cfg.PersistDir, dir)
	if err := os.MkdirAll(persistDir, os.ModePerm); err != nil {
		return err
	}

	persistFile := filepath.Join(persistDir, imageName)
	if cfg.Timeout > 0 {
		persistFile = fmt.Sprintf("%s.timeout%ds", persistFile, cfg.Timeout)
	}

	var hasSentPrefetchList = false

	fanotifyServer := fanotify.NewServer(cfg.ServerPath, container.Pid, imageName, persistFile, cfg.Readable, cfg.Overwrite, time.Duration(cfg.Timeout)*time.Second, logWriter, containerName, hasSentPrefetchList)

	if err := fanotifyServer.RunServer(); err != nil {
		return err
	}

	prefetchDistributionPostEndpoint := fmt.Sprintf("%s%s", cfg.PrefetchDistributionURL, defaultPostEndpoint)

	go func() {
		time.Sleep(10 * time.Minute)
		fanotifyServer.Mu.Lock()
		if !fanotifyServer.IsSent {
			data, err := getPrefetchListfromLocal(persistFile)
			if err != nil {
				log.WithError(err).Error("error reading file")
			}
			if err = sendToServer(imageRepo, containerName, prefetchDistributionPostEndpoint, data); err != nil {
				log.WithError(err).Error("failed to send prefetch to http server")
			}
			fanotifyServer.IsSent = true
		}
		fanotifyServer.Mu.Unlock()
	}()

	globalFanotifyServerLock.Lock()
	globalFanotifyServer[imageName] = fanotifyServer
	globalFanotifyServerLock.Unlock()

	return nil
}

func sendToServer(imageName, containerName, serverURL string, data []byte) error {
	filePaths := strings.Split(string(data), "\n")

	var prefetchFiles []string
	for _, path := range filePaths {
		if path != "" {
			prefetchFiles = append(prefetchFiles, path)
		}
	}

	item := CacheItem{
		ImageName:     imageName,
		ContainerName: containerName,
		PrefetchFiles: prefetchFiles,
	}

	err := postRequest(item, serverURL)
	if err != nil {
		return errors.Wrap(err, "error uploading to server")
	}

	return nil
}

func postRequest(item CacheItem, endpoint string) error {
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}

	resp, err := http.Post(endpoint, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.Wrap(fmt.Errorf("post to server returned a non-OK status code: %d", resp.StatusCode), "HTTP Status Error")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrap(err, "failed to read response body")
	}

	log.Info("Server Response:", string(body))

	return nil
}

func getPrefetchListfromLocal(prefetchListPath string) ([]byte, error) {
	data, err := os.ReadFile(prefetchListPath)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (p *plugin) StopContainer(_ *api.PodSandbox, container *api.Container) ([]*api.ContainerUpdate, error) {
	var update = []*api.ContainerUpdate{}
	_, imageName, imageRepo, err := GetImageName(container.Annotations)
	if err != nil {
		return update, err
	}

	prefetchDistributionPostEndpoint := fmt.Sprintf("%s%s", cfg.PrefetchDistributionURL, defaultPostEndpoint)

	if fanotifyServer, ok := globalFanotifyServer[imageName]; ok {
		fanotifyServer.Mu.Lock()
		if !fanotifyServer.IsSent {
			data, err := getPrefetchListfromLocal(fanotifyServer.PersistFile)
			if err != nil {
				return update, err
			}
			if err = sendToServer(imageRepo, fanotifyServer.ContainerName, prefetchDistributionPostEndpoint, data); err != nil {
				log.WithError(err).Error("failed to send prefetch to http server")
			}
			fanotifyServer.IsSent = true

			fanotifyServer.StopServer()
		}
		fanotifyServer.Mu.Unlock()
	} else {
		return nil, errors.New("can not find fanotify server for container image " + imageName)
	}
	return update, nil
}

func GetImageName(annotations map[string]string) (string, string, string, error) {
	named, err := docker.ParseDockerRef(annotations[imageNameLabel])
	if err != nil {
		return "", "", "", err
	}
	imageRepo := docker.Named.String(named)
	nameTagged := named.(docker.NamedTagged)
	repo := docker.Path(nameTagged)

	dir := filepath.Dir(repo)
	image := filepath.Base(repo)

	imageName := image + ":" + nameTagged.Tag()

	return dir, imageName, imageRepo, nil
}

func (p *plugin) onClose() {
	for _, fanotifyServer := range globalFanotifyServer {
		fanotifyServer.StopServer()
	}
	os.Exit(0)
}

type Handler struct {
	router *mux.Router
}

const endpointImageName string = "/api/v1/imagename"

func (h *Handler) RunUdsServer() error {
	if _, err := os.Stat(filepath.Dir(socketAddr)); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(socketAddr), 0755); err != nil {
			return errors.Wrapf(err, "failed to create directory %s", filepath.Dir(socketAddr))
		}
	}

	if err := os.Remove(socketAddr); err != nil && !os.IsNotExist(err) {
		return errors.Wrapf(err, "failed to remove existing socket file %s", socketAddr)
	}

	listener, err := net.Listen("unix", socketAddr)
	if err != nil {
		return errors.Wrapf(err, "failed to listen socket %s", socketAddr)
	}
	log.Infof("start API server on %s", socketAddr)

	err = http.Serve(listener, h.router)
	if err != nil {
		return errors.Wrapf(err, "system management serving")
	}
	return nil
}

func (h *Handler) registerRouter() {
	h.router.HandleFunc(endpointImageName, h.getPrefetchListfromServer()).Methods(http.MethodPost)
}

func (h *Handler) getPrefetchListfromServer() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Errorf("Failed to read image name: %v", err)
			return
		}

		imageName := string(body)
		getURL := fmt.Sprintf("%s%s?imageName=%s", cfg.PrefetchDistributionURL, defaultGetEndpoint, imageName)

		resp, err := http.Get(getURL)
		if err != nil {
			log.Errorf("Failed to make GET request: %v", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
			log.Errorf("get from server returned a non-OK status code: %d, HTTP Status Error", resp.StatusCode)
			return
		}

		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Errorf("Failed to read response body: %v", err)
			return
		}

		_, err = w.Write(responseBody)
		if err != nil {
			log.Errorf("Failed to write response: %v", err)
			return
		}
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

	h := Handler{
		router: mux.NewRouter(),
	}
	h.registerRouter()
	go func() {
		if err := h.RunUdsServer(); err != nil {
			log.WithError(err).Error("Failed to start image name transformer")
		}
	}()

	if err := app.Run(os.Args); err != nil {
		if errdefs.IsConnectionClosed(err) {
			log.Info("optimizer NRI plugin exited")
		} else {
			log.WithError(err).Fatal("failed to start optimizer NRI plugin")
		}
	}
}
