/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package config

import (
	"os"

	"dario.cat/mergo"
	"github.com/pelletier/go-toml"
	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/internal/constant"
	"github.com/containerd/nydus-snapshotter/internal/flags"
	"github.com/containerd/nydus-snapshotter/pkg/cgroup"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/utils/file"
	"github.com/containerd/nydus-snapshotter/pkg/utils/parser"
	"github.com/containerd/nydus-snapshotter/pkg/utils/sysinfo"
)

func init() {
	recoverPolicyParser = map[string]DaemonRecoverPolicy{
		RecoverPolicyNone.String():     RecoverPolicyNone,
		RecoverPolicyRestart.String():  RecoverPolicyRestart,
		RecoverPolicyFailover.String(): RecoverPolicyFailover}
}

// Define a policy how to fork nydusd daemon and attach file system instances to serve.
type DaemonMode string

const (
	// Spawn a dedicated nydusd for each RAFS instance.
	DaemonModeMultiple DaemonMode = DaemonMode(constant.DaemonModeMultiple)
	// Spawn a dedicated nydusd for each RAFS instance.
	DaemonModeDedicated DaemonMode = DaemonMode(constant.DaemonModeDedicated)
	// Share a global nydusd to serve all RAFS instances.
	DaemonModeShared DaemonMode = DaemonMode(constant.DaemonModeShared)
	// Do not spawn nydusd for RAFS instances.
	//
	// For tarfs and rund, there's no need to create nydusd to serve RAFS instances,
	// the snapshotter just returns mount slices with additional information for runC/runD
	// to manage those snapshots.
	DaemonModeNone    DaemonMode = DaemonMode(constant.DaemonModeNone)
	DaemonModeInvalid DaemonMode = DaemonMode(constant.DaemonModeInvalid)
	// MaxRootPathLen defines the maximum allowed length of the root portion of a Unix domain socket path.
	//
	// This value is calculated based on the hard length limit of `sun_path` on Linux systems (108 bytes).
	// Nydusd's socket path format is "${rootPath}/socket/${xid}/api?.sock".
	// - The length of the fixed part "/socket/${xid}/api?.sock" is 38 bytes.
	//
	// Since the maximum upper limit of the total path length is 108 bytes, in order to avoid exceeding the limit, the maximum allowed length of rootPath is:
	// 108 - len("/socket/${xid}/api?.sock") = 108 - 38 = 70.
	// Therefore, we must set the effective maximum length of the root path to 70 bytes.
	MaxRootPathLen = 70
)

func parseDaemonMode(m string) (DaemonMode, error) {
	switch m {
	case string(DaemonModeMultiple):
		return DaemonModeDedicated, nil
	case string(DaemonModeDedicated):
		return DaemonModeDedicated, nil
	case string(DaemonModeShared):
		return DaemonModeShared, nil
	case string(DaemonModeNone):
		return DaemonModeNone, nil
	default:
		return DaemonModeInvalid, errors.Errorf("invalid daemon mode %q", m)
	}
}

type DaemonRecoverPolicy int

const (
	RecoverPolicyInvalid DaemonRecoverPolicy = iota
	RecoverPolicyNone
	RecoverPolicyRestart
	RecoverPolicyFailover
)

func (p DaemonRecoverPolicy) String() string {
	switch p {
	case RecoverPolicyNone:
		return "none"
	case RecoverPolicyRestart:
		return "restart"
	case RecoverPolicyFailover:
		return "failover"
	case RecoverPolicyInvalid:
		fallthrough
	default:
		return ""
	}
}

var recoverPolicyParser map[string]DaemonRecoverPolicy

func ParseRecoverPolicy(p string) (DaemonRecoverPolicy, error) {
	policy, ok := recoverPolicyParser[p]
	if !ok {
		return RecoverPolicyInvalid, errors.Errorf("invalid recover policy %q", p)
	}

	return policy, nil
}

const (
	FsDriverBlockdev string = constant.FsDriverBlockdev
	FsDriverFusedev  string = constant.FsDriverFusedev
	FsDriverFscache  string = constant.FsDriverFscache
	FsDriverNodev    string = constant.FsDriverNodev
	FsDriverProxy    string = constant.FsDriverProxy
)

type Experimental struct {
	EnableStargz         bool        `toml:"enable_stargz"`
	EnableReferrerDetect bool        `toml:"enable_referrer_detect"`
	TarfsConfig          TarfsConfig `toml:"tarfs"`
	EnableBackendSource  bool        `toml:"enable_backend_source"`
}

type TarfsConfig struct {
	EnableTarfs       bool   `toml:"enable_tarfs"`
	MountTarfsOnHost  bool   `toml:"mount_tarfs_on_host"`
	TarfsHint         bool   `toml:"tarfs_hint"`
	MaxConcurrentProc int    `toml:"max_concurrent_proc"`
	ExportMode        string `toml:"export_mode"`
}

type CgroupConfig struct {
	Enable      bool   `toml:"enable"`
	MemoryLimit string `toml:"memory_limit"`
}

// Configure how to start and recover nydusd daemons
type DaemonConfig struct {
	NydusdPath       string `toml:"nydusd_path"`
	NydusdConfigPath string `toml:"nydusd_config"`
	NydusImagePath   string `toml:"nydusimage_path"`
	RecoverPolicy    string `toml:"recover_policy"`
	FsDriver         string `toml:"fs_driver"`
	ThreadsNumber    int    `toml:"threads_number"`
	LogRotationSize  int    `toml:"log_rotation_size"`
}

type LoggingConfig struct {
	LogToStdout         bool   `toml:"log_to_stdout"`
	LogLevel            string `toml:"level"`
	LogDir              string `toml:"dir"`
	RotateLogMaxSize    int    `toml:"log_rotation_max_size"`
	RotateLogMaxBackups int    `toml:"log_rotation_max_backups"`
	RotateLogMaxAge     int    `toml:"log_rotation_max_age"`
	RotateLogLocalTime  bool   `toml:"log_rotation_local_time"`
	RotateLogCompress   bool   `toml:"log_rotation_compress"`
}

// Nydus image layers additional process
type ImageConfig struct {
	PublicKeyFile     string `toml:"public_key_file"`
	ValidateSignature bool   `toml:"validate_signature"`
}

// Configure containerd snapshots interfaces and how to process the snapshots
// requests from containerd
type SnapshotConfig struct {
	EnableNydusOverlayFS bool   `toml:"enable_nydus_overlayfs"`
	NydusOverlayFSPath   string `toml:"nydus_overlayfs_path"`
	EnableKataVolume     bool   `toml:"enable_kata_volume"`
	SyncRemove           bool   `toml:"sync_remove"`
}

// Configure cache manager that manages the cache files lifecycle
type CacheManagerConfig struct {
	Disable bool `toml:"disable"`
	// Trigger GC gc_period after the specified period.
	// Example format: 24h, 120min
	GCPeriod string `toml:"gc_period"`
	CacheDir string `toml:"cache_dir"`
}

// Configure how nydus-snapshotter receive auth information
type AuthConfig struct {
	// based on kubeconfig or ServiceAccount
	EnableKubeconfigKeychain bool   `toml:"enable_kubeconfig_keychain"`
	KubeconfigPath           string `toml:"kubeconfig_path"`
	// CRI proxy mode
	EnableCRIKeychain   bool   `toml:"enable_cri_keychain"`
	ImageServiceAddress string `toml:"image_service_address"`
}

// Configure remote storage like container registry
type RemoteConfig struct {
	AuthConfig         AuthConfig    `toml:"auth"`
	ConvertVpcRegistry bool          `toml:"convert_vpc_registry"`
	SkipSSLVerify      bool          `toml:"skip_ssl_verify"`
	MirrorsConfig      MirrorsConfig `toml:"mirrors_config"`
}

type MirrorsConfig struct {
	Dir string `toml:"dir"`
}

type MetricsConfig struct {
	Address string `toml:"address"`
}

type DebugConfig struct {
	ProfileDuration int64  `toml:"daemon_cpu_profile_duration_secs"`
	PprofAddress    string `toml:"pprof_address"`
}

type SystemControllerConfig struct {
	Enable      bool        `toml:"enable"`
	Address     string      `toml:"address"`
	DebugConfig DebugConfig `toml:"debug"`
}

type SnapshotterConfig struct {
	// Configuration format version
	Version int `toml:"version"`
	// Snapshotter's root work directory
	Root       string `toml:"root"`
	Address    string `toml:"address"`
	DaemonMode string `toml:"daemon_mode"`
	// Clean up all the resources when snapshotter is closed
	CleanupOnClose bool `toml:"cleanup_on_close"`

	SystemControllerConfig SystemControllerConfig `toml:"system"`
	MetricsConfig          MetricsConfig          `toml:"metrics"`
	DaemonConfig           DaemonConfig           `toml:"daemon"`
	SnapshotsConfig        SnapshotConfig         `toml:"snapshot"`
	RemoteConfig           RemoteConfig           `toml:"remote"`
	ImageConfig            ImageConfig            `toml:"image"`
	CacheManagerConfig     CacheManagerConfig     `toml:"cache_manager"`
	LoggingConfig          LoggingConfig          `toml:"log"`
	CgroupConfig           CgroupConfig           `toml:"cgroup"`
	Experimental           Experimental           `toml:"experimental"`
}

func LoadSnapshotterConfig(path string) (*SnapshotterConfig, error) {
	var config SnapshotterConfig
	// get nydus-snapshotter configuration from specified path of toml file
	if path == "" {
		return nil, errors.New("snapshotter configuration path cannot be empty")
	}
	tree, err := toml.LoadFile(path)
	if err != nil {
		return nil, errors.Wrapf(err, "load toml configuration from file %q", path)
	}

	if err = tree.Unmarshal(&config); err != nil {
		return nil, errors.Wrap(err, "unmarshal snapshotter configuration")
	}
	if config.Version != 1 {
		return nil, errors.Errorf("unsupported configuration version %d", config.Version)
	}
	return &config, nil
}

func MergeConfig(to, from *SnapshotterConfig) error {
	err := mergo.Merge(to, from)
	if err != nil {
		return err
	}

	return nil
}

func ValidateConfig(c *SnapshotterConfig) error {
	if c == nil {
		return errors.Wrapf(errdefs.ErrInvalidArgument, "configuration is none")
	}

	if c.ImageConfig.ValidateSignature {
		if c.ImageConfig.PublicKeyFile == "" {
			return errors.New("public key file for signature validation is not provided")
		} else if _, err := os.Stat(c.ImageConfig.PublicKeyFile); err != nil {
			return errors.Wrapf(err, "check publicKey file %q", c.ImageConfig.PublicKeyFile)
		}
	}

	rootPathLen := len(c.Root)
	if rootPathLen == 0 {
		return errors.New("empty root directory")
	}
	if rootPathLen > MaxRootPathLen {
		return errors.Errorf("root directory path is too long: %d bytes, max is %d bytes", rootPathLen, MaxRootPathLen)
	}

	if c.DaemonConfig.FsDriver != FsDriverFscache && c.DaemonConfig.FsDriver != FsDriverFusedev &&
		c.DaemonConfig.FsDriver != FsDriverBlockdev && c.DaemonConfig.FsDriver != FsDriverNodev &&
		c.DaemonConfig.FsDriver != FsDriverProxy {
		return errors.Errorf("invalid filesystem driver %q", c.DaemonConfig.FsDriver)
	}
	if _, err := ParseRecoverPolicy(c.DaemonConfig.RecoverPolicy); err != nil {
		return err
	}
	if c.DaemonConfig.ThreadsNumber > 1024 {
		return errors.Errorf("nydusd worker thread number %d is too big, max 1024", c.DaemonConfig.ThreadsNumber)
	}

	if c.RemoteConfig.AuthConfig.EnableCRIKeychain && c.RemoteConfig.AuthConfig.EnableKubeconfigKeychain {
		return errors.Wrapf(errdefs.ErrInvalidArgument,
			"\"enable_cri_keychain\" and \"enable_kubeconfig_keychain\" can't be set at the same time")
	}

	if c.RemoteConfig.MirrorsConfig.Dir != "" {
		dirExisted, err := file.IsDirExisted(c.RemoteConfig.MirrorsConfig.Dir)
		if err != nil {
			return err
		}
		if !dirExisted {
			return errors.Errorf("mirrors config directory %s does not exist", c.RemoteConfig.MirrorsConfig.Dir)
		}
	}

	return nil
}

// Parse command line arguments and fill the nydus-snapshotter configuration
// Always let options from CLI override those from configuration file.
func ParseParameters(args *flags.Args, cfg *SnapshotterConfig) error {
	// --- essential configuration
	if args.Address != "" {
		cfg.Address = args.Address
	}
	if args.RootDir != "" {
		cfg.Root = args.RootDir
	}

	// Give --shared-daemon higher priority
	if args.DaemonMode != "" {
		cfg.DaemonMode = args.DaemonMode
	}

	// --- image processor configuration
	// empty

	// --- daemon configuration
	daemonConfig := &cfg.DaemonConfig
	if args.NydusdConfigPath != "" {
		daemonConfig.NydusdConfigPath = args.NydusdConfigPath
	}
	if args.NydusdPath != "" {
		daemonConfig.NydusdPath = args.NydusdPath
	}
	if args.NydusImagePath != "" {
		daemonConfig.NydusImagePath = args.NydusImagePath
	}
	if args.FsDriver != "" {
		daemonConfig.FsDriver = args.FsDriver
	}

	// --- cache manager configuration
	// empty

	// --- logging configuration
	logConfig := &cfg.LoggingConfig
	if args.LogLevel != "" {
		logConfig.LogLevel = args.LogLevel
	}
	if args.LogToStdoutCount > 0 {
		logConfig.LogToStdout = args.LogToStdout
	}

	// --- remote storage configuration
	// empty

	// --- snapshot configuration
	if args.NydusOverlayFSPath != "" {
		cfg.SnapshotsConfig.NydusOverlayFSPath = args.NydusOverlayFSPath
	}

	// --- metrics configuration
	// empty

	return nil
}

func ParseCgroupConfig(config CgroupConfig) (cgroup.Config, error) {
	totalMemory, err := sysinfo.GetTotalMemoryBytes()
	if err != nil {
		return cgroup.Config{}, errors.Wrap(err, "Failed  to get total memory bytes")
	}

	memoryLimitInBytes, err := parser.MemoryConfigToBytes(config.MemoryLimit, totalMemory)
	if err != nil {
		return cgroup.Config{}, err
	}

	return cgroup.Config{
		MemoryLimitInBytes: memoryLimitInBytes,
	}, nil
}
