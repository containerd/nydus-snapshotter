package snapshotter

import (
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/snapshot"
)

func init() {
	registry.Register(&plugin.Registration{
		Type:   plugins.SnapshotPlugin,
		ID:     "nydus",
		Config: &config.SnapshotterConfig{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ic.Meta.Platforms = append(ic.Meta.Platforms, platforms.DefaultSpec())

			cfg, ok := ic.Config.(*config.SnapshotterConfig)
			if !ok {
				return nil, errors.New("invalid nydus snapshotter configuration")
			}

			root := ic.Properties[plugins.PropertyRootDir]
			if root == "" {
				cfg.Root = root
			}

			if err := cfg.FillUpWithDefaults(); err != nil {
				return nil, errors.New("failed to fill up nydus configuration with defaults")
			}

			rs, err := snapshot.NewSnapshotter(ic.Context, cfg)
			if err != nil {
				return nil, errors.Wrap(err, "failed to initialize snapshotter")
			}
			return rs, nil

		},
	})
}
