package snapshot

import (
	"context"
	"os"
	"testing"

	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/converter"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

var _ converter.Converter = &fakeConvert{}

type fakeConvert struct {
	Converted bool
}

func (f *fakeConvert) Init(ctx context.Context) error { return nil }
func (f *fakeConvert) Convert(ctx context.Context, source string, manifestDigest digest.Digest,
	currentLayerDigest digest.Digest, blobPath string) error {
	f.Converted = true
	return nil
}
func (f *fakeConvert) Merge(ctx context.Context, blobs []string, bootstrapPath string) error {
	return nil
}
func (f *fakeConvert) BlobDir() string                { return "" }
func (f *fakeConvert) DeleteImage(any interface{})    {}
func (f *fakeConvert) DeleteManifest(any interface{}) {}

func TestNewSnapshotter(t *testing.T) {
	{
		tmpDir, err := os.MkdirTemp(os.TempDir(), "snapshotter")
		require.NoError(t, err)
		defer func() {
			os.RemoveAll(tmpDir)
		}()
		_, err = NewSnapshotter(context.TODO(), &config.Config{
			RootDir:             tmpDir,
			DaemonMode:          config.DaemonModeNone,
			DisableCacheManager: true,
			DaemonCfg: config.DaemonConfig{
				Device: config.DeviceConfig{
					Backend: struct {
						BackendType string               `json:"type"`
						Config      config.BackendConfig `json:"config"`
					}{
						BackendType: "localfs",
						Config: config.BackendConfig{
							Dir: "/home/t4/containerd/io.containerd.snapshotter.v1.nydus/blobs",
						},
					},
				},
			},
			LogToStdout: true,
			LogDir:      tmpDir,
		})

		require.NoError(t, err)
	}

	{
		tmpDir, err := os.MkdirTemp(os.TempDir(), "snapshotter")
		require.NoError(t, err)
		defer func() {
			os.RemoveAll(tmpDir)
		}()
		_, err = NewSnapshotter(context.TODO(), &config.Config{
			RootDir:             tmpDir,
			DaemonMode:          config.DaemonModeNone,
			DisableCacheManager: true,
			DaemonCfg: config.DaemonConfig{
				Device: config.DeviceConfig{
					Backend: struct {
						BackendType string               `json:"type"`
						Config      config.BackendConfig `json:"config"`
					}{
						BackendType: "localfs",
						Config: config.BackendConfig{
							Dir: "/home/t4/containerd/io.containerd.snapshotter.v1.nydus/blobs",
						},
					},
				},
			},
			LogToStdout:       true,
			LogDir:            tmpDir,
			ContainerdAddress: "/invalid-path",
		})
		// Even if containerd does not exist, no error will be returned here,
		// but the initialization fails, and it will be initialized again in the subsequent Prepare
		require.NoError(t, err)
	}

}
func TestPrepare(t *testing.T) {
	tmpDir, err := os.MkdirTemp(os.TempDir(), "snapshotter")
	require.NoError(t, err)
	defer func() {
		os.RemoveAll(tmpDir)
	}()
	sn, err := NewSnapshotter(context.TODO(), &config.Config{
		RootDir:             tmpDir,
		DaemonMode:          config.DaemonModeNone,
		DisableCacheManager: true,
		DaemonCfg: config.DaemonConfig{
			Device: config.DeviceConfig{
				Backend: struct {
					BackendType string               `json:"type"`
					Config      config.BackendConfig `json:"config"`
				}{
					BackendType: "localfs",
					Config: config.BackendConfig{
						Dir: "/home/t4/containerd/io.containerd.snapshotter.v1.nydus/blobs",
					},
				},
			},
		},
		LogToStdout:       true,
		LogDir:            tmpDir,
		ContainerdAddress: "/invalid-path",
	})
	// Even if containerd does not exist, no error will be returned here,
	// but the initialization fails, and it will be initialized again in the subsequent Prepare
	require.NoError(t, err)

	realSn, ok := sn.(*snapshotter)
	require.True(t, ok)
	fakeConverter := &fakeConvert{}
	realSn.converter = fakeConverter
	opt := snapshots.WithLabels(map[string]string{
		label.CRIImageLayers:    "sha256:50783e0dfb64b73019e973e7bce2c0d5a882301b781327ca153b876ad758dbd3",
		label.CRIImageRef:       "docker.io/library/busybox:latest",
		label.CRILayerDigest:    "sha256:50783e0dfb64b73019e973e7bce2c0d5a882301b781327ca153b876ad758dbd3",
		label.CRIManifestDigest: "sha256:98de1ad411c6d08e50f26f392f3bc6cd65f686469b7c22a85c7b5fb1b820c154",
		label.TargetSnapshotRef: "sha256:084326605ab6715ca698453e530e4d0319d4e402b468894a06affef944b4ef04",
	})

	_, err = sn.Prepare(context.Background(), "fake-key", "", opt)
	require.Error(t, err)
	// Image converted
	require.True(t, fakeConverter.Converted)
}
