package optimizer

import (
	"fmt"
	"log/syslog"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/containerd/reference/docker"
	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nydus-snapshotter/pkg/optimizer/ebpf"
	"github.com/containerd/nydus-snapshotter/pkg/optimizer/fanotify"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type Server interface {
	Start() error
	Stop()
}

const (
	imageNameLabel = "io.kubernetes.cri.image-name"
)

const (
	EBPF     = "ebpf"
	FANOTIFY = "fanotify"
)

type Config struct {
	ServerType string `toml:"server_type"`
	ServerPath string `toml:"server_path"`
	PersistDir string `toml:"persist_dir"`
	Readable   bool   `toml:"readable"`
	Timeout    int    `toml:"timeout"`
	Overwrite  bool   `toml:"overwrite"`
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

func getPersistPath(cfg Config, dir, imageName string) (string, error) {
	persistDir := filepath.Join(cfg.PersistDir, dir)
	if err := os.MkdirAll(persistDir, os.ModePerm); err != nil {
		return "", err
	}

	persistFile := filepath.Join(persistDir, imageName)
	if cfg.Timeout > 0 {
		persistFile = fmt.Sprintf("%s.timeout%ds", persistFile, cfg.Timeout)
	}

	return persistFile, nil
}

func getPersistFile(persistFile string) (*os.File, *os.File, error) {
	f, err := os.OpenFile(persistFile, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to open file %q", persistFile)
	}

	persistCsvFile := fmt.Sprintf("%s.csv", persistFile)
	fCsv, err := os.Create(persistCsvFile)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to create file %q", persistCsvFile)
	}

	return f, fCsv, nil
}

func NewServer(cfg Config, container *api.Container, logWriter *syslog.Writer) (string, Server, error) {
	dir, imageName, err := GetImageName(container.Annotations)
	if err != nil {
		return "", nil, err
	}

	persistPath, err := getPersistPath(cfg, dir, imageName)
	if err != nil {
		return "", nil, err
	}

	if !cfg.Overwrite {
		if file, err := os.Stat(persistPath); err == nil && !file.IsDir() {
			return imageName, nil, nil
		}
	}

	file, csvFile, err := getPersistFile(persistPath)
	if err != nil {
		return "", nil, err
	}

	logrus.Infof("start optimizer server for %s, image: %s, persist file: %s", container.Id, imageName, persistPath)
	if cfg.ServerType == EBPF {
		return imageName, ebpf.NewServer(container.Id, imageName, file, csvFile, cfg.Readable, cfg.Overwrite, time.Duration(cfg.Timeout)*time.Second, logWriter), nil
	}
	return imageName, fanotify.NewServer(cfg.ServerPath, container.Pid, imageName, file, csvFile, cfg.Readable, cfg.Overwrite, time.Duration(cfg.Timeout)*time.Second, logWriter), nil
}
