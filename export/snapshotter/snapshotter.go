package snapshotter

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"

	"github.com/containerd/nydus-snapshotter/internal/config"
	"github.com/containerd/nydus-snapshotter/internal/containerd-nydus-grpc/command"
	"github.com/containerd/nydus-snapshotter/snapshot"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type:   plugin.SnapshotPlugin,
		ID:     "nydus",
		Config: &config.Config{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ic.Meta.Platforms = append(ic.Meta.Platforms, platforms.DefaultSpec())
			cfg, ok := ic.Config.(*config.Config)
			if !ok {
				return nil, errors.New("invalid nydus snapshotter configuration")
			}

			if err := initConfig(cfg, ic); err != nil {
				return nil, fmt.Errorf("failed to initialize config: %w", err)
			}

			rs, err := snapshot.NewSnapshotter(ic.Context, cfg)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize snapshotter: %w", err)
			}

			return rs, nil
		},
	})
}

// initConfig initializes snapshotter config with containerd.
func initConfig(cfg *config.Config, ic *plugin.InitContext) error {
	if cfg.RootDir == "" {
		cfg.RootDir = ic.Root
	}

	if cfg.LogLevel == "" {
		cfg.LogLevel = command.DefaultLogLevel.String()
	}

	if cfg.DaemonCfgPath == "" {
		cfg.DaemonCfgPath = command.DefaultDaemonConfigPath
	}

	if cfg.DaemonMode == "" {
		cfg.DaemonMode = command.DefaultDaemonMode
	}

	if cfg.GCPeriod == 0 {
		cfg.GCPeriod = command.DefaultGCPeriod
	}

	if len(cfg.CacheDir) == 0 {
		cfg.CacheDir = filepath.Join(cfg.RootDir, "cache")
	}

	if len(cfg.LogDir) == 0 {
		cfg.LogDir = filepath.Join(cfg.RootDir, command.DefaultLogDirName)
	}

	return cfg.SetupNydusBinaryPaths()
}
