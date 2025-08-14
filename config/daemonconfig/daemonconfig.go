/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemonconfig

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"

	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/utils/registry"
)

type StorageBackendType = string

const (
	backendTypeLocalfs  StorageBackendType = "localfs"
	backendTypeOss      StorageBackendType = "oss"
	backendTypeRegistry StorageBackendType = "registry"
	backendTypeS3       StorageBackendType = "s3"
)

type DaemonConfig interface {
	// Provide stuffs relevant to accessing registry apart from auth
	Supplement(host, repo, snapshotID string, params map[string]string)
	// Provide auth
	FillAuth(kc *auth.PassKeyChain)
	StorageBackend() (StorageBackendType, *BackendConfig)
	UpdateMirrors(mirrorsConfigDir, registryHost string) error
	DumpString() (string, error)
	DumpFile(path string) error
}

// Daemon configurations factory
func NewDaemonConfig(fsDriver, path string) (DaemonConfig, error) {
	switch fsDriver {
	case config.FsDriverFscache:
		cfg, err := LoadFscacheConfig(path)
		if err != nil {
			return nil, err
		}
		return cfg, nil
	case config.FsDriverFusedev:
		cfg, err := LoadFuseConfig(path)
		if err != nil {
			return nil, err
		}
		return cfg, nil
	default:
		return nil, errors.Errorf("unsupported, fs driver %q", fsDriver)
	}
}

type MirrorConfig struct {
	Host                string            `json:"host,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	HealthCheckInterval int               `json:"health_check_interval,omitempty"`
	FailureLimit        uint8             `json:"failure_limit,omitempty"`
	PingURL             string            `json:"ping_url,omitempty"`
}

type BackendConfig struct {
	// Localfs backend configs
	BlobFile     string `json:"blob_file,omitempty"`
	Dir          string `json:"dir,omitempty"`
	ReadAhead    bool   `json:"readahead"`
	ReadAheadSec int    `json:"readahead_sec,omitempty"`

	// Registry backend configs
	Host               string         `json:"host,omitempty"`
	Repo               string         `json:"repo,omitempty"`
	Auth               string         `json:"auth,omitempty" secret:"true"`
	RegistryToken      string         `json:"registry_token,omitempty" secret:"true"`
	BlobURLScheme      string         `json:"blob_url_scheme,omitempty"`
	BlobRedirectedHost string         `json:"blob_redirected_host,omitempty"`
	Mirrors            []MirrorConfig `json:"mirrors,omitempty"`

	// Shared by oss and s3 backend configs
	EndPoint        string `json:"endpoint,omitempty"`
	AccessKeyID     string `json:"access_key_id,omitempty" secret:"true"`
	AccessKeySecret string `json:"access_key_secret,omitempty" secret:"true"`
	BucketName      string `json:"bucket_name,omitempty"`
	ObjectPrefix    string `json:"object_prefix,omitempty"`

	// S3-specific config
	Region string `json:"region,omitempty"`

	// Shared by registry, oss, and s3
	Scheme     string `json:"scheme,omitempty"`
	SkipVerify bool   `json:"skip_verify,omitempty"`

	// Below configs are common configs shared by all backends
	Proxy struct {
		URL           string `json:"url,omitempty"`
		Fallback      bool   `json:"fallback"`
		PingURL       string `json:"ping_url,omitempty"`
		CheckInterval int    `json:"check_interval,omitempty"`
		UseHTTP       bool   `json:"use_http,omitempty"`
	} `json:"proxy,omitempty"`
	Timeout        int `json:"timeout,omitempty"`
	ConnectTimeout int `json:"connect_timeout,omitempty"`
	RetryLimit     int `json:"retry_limit,omitempty"`
}

type DeviceConfig struct {
	Backend struct {
		BackendType string        `json:"type"`
		Config      BackendConfig `json:"config"`
	} `json:"backend"`
	Cache struct {
		CacheType  string `json:"type"`
		Compressed bool   `json:"compressed,omitempty"`
		Config     struct {
			WorkDir           string `json:"work_dir"`
			DisableIndexedMap bool   `json:"disable_indexed_map"`
		} `json:"config"`
	} `json:"cache"`
}

// For nydusd as FUSE daemon. Serialize Daemon info and persist to a json file
// We don't have to persist configuration file for fscache since its configuration
// is passed through HTTP API.
func DumpConfigFile(c interface{}, path string) error {
	if config.IsBackendSourceEnabled() {
		c = serializeWithSecretFilter(c)
	}
	b, err := json.Marshal(c)
	if err != nil {
		return errors.Wrapf(err, "marshal config")
	}

	return os.WriteFile(path, b, 0600)
}

func DumpConfigString(c interface{}) (string, error) {
	b, err := json.Marshal(c)
	return string(b), err
}

// Achieve a daemon configuration from template or snapshotter's configuration
func SupplementDaemonConfig(c DaemonConfig, imageID, snapshotID string,
	vpcRegistry bool, labels map[string]string, params map[string]string) error {

	image, err := registry.ParseImage(imageID)
	if err != nil {
		return errors.Wrapf(err, "parse image %s", imageID)
	}

	backendType, _ := c.StorageBackend()

	switch backendType {
	case backendTypeRegistry:
		registryHost := image.Host
		if vpcRegistry {
			registryHost = registry.ConvertToVPCHost(registryHost)
		} else if registryHost == "docker.io" {
			// For docker.io images, we should use index.docker.io
			registryHost = "index.docker.io"
		}

		if err := c.UpdateMirrors(config.GetMirrorsConfigDir(), registryHost); err != nil {
			return errors.Wrap(err, "update mirrors config")
		}

		// If no auth is provided, don't touch auth from provided nydusd configuration file.
		// We don't validate the original nydusd auth from configuration file since it can be empty
		// when repository is public.
		keyChain := auth.GetRegistryKeyChain(registryHost, imageID, labels)
		c.Supplement(registryHost, image.Repo, snapshotID, params)
		c.FillAuth(keyChain)

	// For Localfs, OSS, and S3 backends, only the WorkDir needs to be supplemented.
	case backendTypeLocalfs, backendTypeOss, backendTypeS3:
		c.Supplement("", "", snapshotID, params)
	default:
		return errors.Errorf("unknown backend type %s", backendType)
	}

	return nil
}

func serializeWithSecretFilter(obj interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	value := reflect.ValueOf(obj)
	typeOfObj := reflect.TypeOf(obj)

	if value.Kind() == reflect.Ptr {
		value = value.Elem()
		typeOfObj = typeOfObj.Elem()
	}

	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		fieldType := typeOfObj.Field(i)
		secretTag := fieldType.Tag.Get("secret")
		jsonTags := strings.Split(fieldType.Tag.Get("json"), ",")
		omitemptyTag := false

		for _, tag := range jsonTags {
			if tag == "omitempty" {
				omitemptyTag = true
				break
			}
		}

		if secretTag == "true" {
			continue
		}

		if field.Kind() == reflect.Ptr && field.IsNil() {
			continue
		}

		if omitemptyTag && reflect.DeepEqual(reflect.Zero(field.Type()).Interface(), field.Interface()) {
			continue
		}

		//nolint:exhaustive
		switch fieldType.Type.Kind() {
		case reflect.Struct:
			result[jsonTags[0]] = serializeWithSecretFilter(field.Interface())
		case reflect.Ptr:
			result[jsonTags[0]] = serializeWithSecretFilter(field.Elem().Interface())
		default:
			result[jsonTags[0]] = field.Interface()
		}
	}

	return result
}
