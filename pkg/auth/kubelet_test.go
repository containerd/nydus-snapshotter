/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeletconfigv1 "k8s.io/kubelet/config/v1"
	credentialproviderv1 "k8s.io/kubelet/pkg/apis/credentialprovider/v1"
	"sigs.k8s.io/yaml"
)

const (
	testUser = "test-user"
	testPass = "test-pass"
)

type credentialMap map[string]struct{ username, password string }

// setupKubeletProvider creates a temporary directory with mock credential provider
// plugins and configuration file for testing.
func setupKubeletProvider(t *testing.T) (configPath, binDir string) {
	t.Helper()

	// Create temporary directories
	tmpDir := t.TempDir()

	binDir = filepath.Join(tmpDir, "bin")
	err := os.Mkdir(binDir, 0755)
	require.NoError(t, err)

	configPath = filepath.Join(tmpDir, "credential-config.yaml")

	return configPath, binDir
}

// buildAuthSection builds a JSON auth map for a mock plugin response.
func buildAuthSection(registries credentialMap) string {
	if len(registries) == 0 {
		return "null"
	}
	auth := "{"
	first := true
	for registry, creds := range registries {
		if !first {
			auth += ","
		}
		auth += fmt.Sprintf(`"%s":{"username":"%s","password":"%s"}`,
			registry, creds.username, creds.password)
		first = false
	}
	return auth + "}"
}

// createMockPlugin creates a mock credential provider plugin that returns
// credentials based on the input image. If registries is empty, return null auth.
// Uses a 10m cacheDuration so results are cached by default.
func createMockPlugin(t *testing.T, binDir, pluginName string, registries credentialMap) {
	t.Helper()
	createMockPluginWithTTL(t, binDir, pluginName, registries, "10m")
}

// createMockPluginWithTTL is like createMockPlugin but lets the caller control
// the cacheDuration field in the response (e.g. "0s" to disable caching).
func createMockPluginWithTTL(t *testing.T, binDir, pluginName string, registries credentialMap, cacheDuration string) {
	t.Helper()
	createMockPluginFull(t, binDir, pluginName, registries, cacheDuration, "Image")
}

// createMockPluginFull creates a mock plugin with full control over cacheDuration
// and cacheKeyType fields in the response.
func createMockPluginFull(t *testing.T, binDir, pluginName string, registries credentialMap, cacheDuration, cacheKeyType string) {
	t.Helper()

	script := fmt.Sprintf(`#!/bin/bash
cat > /dev/null
cat <<'EOF'
{"kind":"CredentialProviderResponse","apiVersion":"credentialprovider.kubelet.k8s.io/v1","cacheKeyType":%q,"cacheDuration":%q,"auth":%s}
EOF
`, cacheKeyType, cacheDuration, buildAuthSection(registries))

	err := os.WriteFile(filepath.Join(binDir, pluginName), []byte(script), 0755)
	require.NoError(t, err)
}

// createMockCredentialProvider creates a CredentialProvider with the given parameters.
// Uses 10m as the default cache duration.
func createMockCredentialProvider(name string, matchImages []string) kubeletconfigv1.CredentialProvider {
	return createMockCredentialProviderWithTTL(name, matchImages, 10*time.Minute)
}

// createMockCredentialProviderWithTTL is like createMockCredentialProvider but
// lets the caller control the DefaultCacheDuration.
func createMockCredentialProviderWithTTL(name string, matchImages []string, defaultTTL time.Duration) kubeletconfigv1.CredentialProvider {
	return kubeletconfigv1.CredentialProvider{
		Name:                 name,
		APIVersion:           "credentialprovider.kubelet.k8s.io/v1",
		DefaultCacheDuration: &metav1.Duration{Duration: defaultTTL},
		MatchImages:          matchImages,
	}
}

// createMockProviderConfig creates a credential provider configuration file.
func createMockProviderConfig(t *testing.T, configPath string, providers []kubeletconfigv1.CredentialProvider) {
	t.Helper()

	config := kubeletconfigv1.CredentialProviderConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "kubelet.config.k8s.io/v1",
			Kind:       "CredentialProviderConfig",
		},
		Providers: providers,
	}

	data, err := yaml.Marshal(config)
	require.NoError(t, err)

	err = os.WriteFile(configPath, data, 0644)
	require.NoError(t, err)
}

// setupMockProvider creates a complete mock plugin + config for testing.
func setupMockProvider(t *testing.T, binDir, pluginName, configPath string, registries credentialMap, matchImages []string) {
	t.Helper()
	createMockPlugin(t, binDir, pluginName, registries)
	createMockProviderConfig(t, configPath, []kubeletconfigv1.CredentialProvider{
		createMockCredentialProvider(pluginName, matchImages),
	})
}

func TestNewKubeletProvider(t *testing.T) {
	configPath, binDir := setupKubeletProvider(t)

	tests := []struct {
		name        string
		setup       func(t *testing.T) (configPath, binDir string)
		wantErr     bool
		errContains string
		validate    func(t *testing.T, provider *KubeletProvider)
	}{
		{
			name: "successful initialization",
			setup: func(t *testing.T) (string, string) {
				setupMockProvider(t, binDir, "test-plugin", configPath,
					credentialMap{"registry.example.com": {testUser, testPass}},
					[]string{"*.example.com"})
				return configPath, binDir
			},
			validate: func(t *testing.T, provider *KubeletProvider) {
				assert.Len(t, provider.plugins, 1)
				assert.Equal(t, "test-plugin", provider.plugins[0].Name)
			},
		},
		{
			name: "empty config path",
			setup: func(t *testing.T) (string, string) {
				return "", binDir
			},
			wantErr:     true,
			errContains: "config path cannot be empty",
		},
		{
			name: "empty bin directory",
			setup: func(t *testing.T) (string, string) {
				return configPath, ""
			},
			wantErr:     true,
			errContains: "bin directory cannot be empty",
		},
		{
			name: "nonexistent config file",
			setup: func(t *testing.T) (string, string) {
				return "/nonexistent/config.yaml", binDir
			},
			wantErr: true,
		},
		{
			name: "invalid config format",
			setup: func(t *testing.T) (string, string) {
				invalidConfigPath := filepath.Join(binDir, "invalid.yaml")
				err := os.WriteFile(invalidConfigPath, []byte("invalid: yaml: content: ["), 0644)
				require.NoError(t, err)
				return invalidConfigPath, binDir
			},
			wantErr: true,
		},
		{
			name: "no providers in config",
			setup: func(t *testing.T) (string, string) {
				emptyConfigPath := filepath.Join(binDir, "empty.yaml")
				err := os.WriteFile(emptyConfigPath, []byte(`apiVersion: kubelet.config.k8s.io/v1
kind: CredentialProviderConfig
providers: []
`), 0644)
				require.NoError(t, err)
				return emptyConfigPath, binDir
			},
			wantErr:     true,
			errContains: "at least one provider is required",
		},
		{
			name: "duplicate provider names",
			setup: func(t *testing.T) (string, string) {
				createMockPlugin(t, binDir, "duplicate-plugin", credentialMap{"registry.example.com": {testUser, testPass}})
				dupConfigPath := filepath.Join(binDir, "duplicate.yaml")
				createMockProviderConfig(t, dupConfigPath, []kubeletconfigv1.CredentialProvider{
					createMockCredentialProvider("duplicate-plugin", []string{"*.example.com"}),
					createMockCredentialProvider("duplicate-plugin", []string{"*.docker.io"}),
				})
				return dupConfigPath, binDir
			},
			wantErr:     true,
			errContains: "duplicate provider name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, binDir := tt.setup(t)
			provider, err := NewKubeletProvider(configPath, binDir)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, provider)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, provider)
			if tt.validate != nil {
				tt.validate(t, provider)
			}
		})
	}
}

func TestValidateCredentialProvider(t *testing.T) {
	tests := []struct {
		name        string
		setup       func() *kubeletconfigv1.CredentialProvider
		wantErr     bool
		errContains string
	}{
		{
			name: "valid provider",
			setup: func() *kubeletconfigv1.CredentialProvider {
				provider := createMockCredentialProvider("valid-plugin", []string{"*.example.com"})
				return &provider
			},
			wantErr: false,
		},
		{
			name: "empty provider name",
			setup: func() *kubeletconfigv1.CredentialProvider {
				provider := createMockCredentialProvider("valid-plugin", []string{"*.example.com"})
				provider.Name = ""
				return &provider
			},
			wantErr:     true,
			errContains: "provider name is required",
		},
		{
			name: "invalid provider name with path separator",
			setup: func() *kubeletconfigv1.CredentialProvider {
				provider := createMockCredentialProvider("valid-plugin", []string{"*.example.com"})
				provider.Name = "invalid/name"
				return &provider
			},
			wantErr:     true,
			errContains: "cannot contain spaces, path separators",
		},
		{
			name: "provider name with space",
			setup: func() *kubeletconfigv1.CredentialProvider {
				provider := createMockCredentialProvider("valid-plugin", []string{"*.example.com"})
				provider.Name = "invalid name"
				return &provider
			},
			wantErr:     true,
			errContains: "cannot contain spaces, path separators",
		},
		{
			name: "provider name is dot",
			setup: func() *kubeletconfigv1.CredentialProvider {
				provider := createMockCredentialProvider("valid-plugin", []string{"*.example.com"})
				provider.Name = "."
				return &provider
			},
			wantErr:     true,
			errContains: "cannot contain spaces, path separators",
		},
		{
			name: "empty API version",
			setup: func() *kubeletconfigv1.CredentialProvider {
				provider := createMockCredentialProvider("valid-plugin", []string{"*.example.com"})
				provider.APIVersion = ""
				return &provider
			},
			wantErr:     true,
			errContains: "apiVersion is required",
		},
		{
			name: "unsupported API version",
			setup: func() *kubeletconfigv1.CredentialProvider {
				provider := createMockCredentialProvider("valid-plugin", []string{"*.example.com"})
				provider.APIVersion = "credentialprovider.kubelet.k8s.io/v2"
				return &provider
			},
			wantErr:     true,
			errContains: "unsupported apiVersion",
		},
		{
			name: "no match images",
			setup: func() *kubeletconfigv1.CredentialProvider {
				provider := createMockCredentialProvider("valid-plugin", []string{"*.example.com"})
				provider.MatchImages = []string{}
				return &provider
			},
			wantErr:     true,
			errContains: "at least one matchImage is required",
		},
		{
			name: "empty match image pattern",
			setup: func() *kubeletconfigv1.CredentialProvider {
				provider := createMockCredentialProvider("valid-plugin", []string{"*.example.com"})
				provider.MatchImages = []string{"valid.com", ""}
				return &provider
			},
			wantErr:     true,
			errContains: "empty matchImage pattern",
		},
		{
			name: "nil cache duration",
			setup: func() *kubeletconfigv1.CredentialProvider {
				provider := createMockCredentialProvider("valid-plugin", []string{"*.example.com"})
				provider.DefaultCacheDuration = nil
				return &provider
			},
			wantErr:     true,
			errContains: "defaultCacheDuration is required",
		},
		{
			name: "negative cache duration",
			setup: func() *kubeletconfigv1.CredentialProvider {
				provider := createMockCredentialProvider("valid-plugin", []string{"*.example.com"})
				duration := metav1.Duration{Duration: -1 * time.Minute}
				provider.DefaultCacheDuration = &duration
				return &provider
			},
			wantErr:     true,
			errContains: "defaultCacheDuration must be >= 0",
		},
		{
			name: "empty environment variable name",
			setup: func() *kubeletconfigv1.CredentialProvider {
				provider := createMockCredentialProvider("valid-plugin", []string{"*.example.com"})
				provider.Env = []kubeletconfigv1.ExecEnvVar{
					{Name: "", Value: "value1"},
				}
				return &provider
			},
			wantErr:     true,
			errContains: "environment variable name cannot be empty",
		},
		{
			name: "duplicate environment variable names",
			setup: func() *kubeletconfigv1.CredentialProvider {
				provider := createMockCredentialProvider("valid-plugin", []string{"*.example.com"})
				provider.Env = []kubeletconfigv1.ExecEnvVar{
					{Name: "VAR1", Value: "value1"},
					{Name: "VAR1", Value: "value2"},
				}
				return &provider
			},
			wantErr:     true,
			errContains: "duplicate environment variable name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := tt.setup()
			err := validateCredentialProvider(provider)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			assert.NoError(t, err)
		})
	}
}

func TestKubeletProviderGetCredentials(t *testing.T) {
	// Save and restore the global kubeletProvider
	oldProvider := kubeletProvider
	defer func() { kubeletProvider = oldProvider }()

	configPath, binDir := setupKubeletProvider(t)

	tests := []struct {
		name         string
		setup        func(t *testing.T) *KubeletProvider
		request      *AuthRequest
		wantErr      bool
		errContains  string
		wantUsername string
		wantPassword string
		wantNil      bool
	}{
		{
			name: "nil request",
			setup: func(t *testing.T) *KubeletProvider {
				setupMockProvider(t, binDir, "test-plugin", configPath,
					credentialMap{"registry.example.com": {testUser, testPass}},
					[]string{"*.example.com"})
				provider, err := NewKubeletProvider(configPath, binDir)
				require.NoError(t, err)
				return provider
			},
			request:     nil,
			wantErr:     true,
			errContains: "ref not found",
		},
		{
			name: "empty ref",
			setup: func(t *testing.T) *KubeletProvider {
				cfg := filepath.Join(binDir, "config2.yaml")
				setupMockProvider(t, binDir, "test-plugin-2", cfg,
					credentialMap{"registry.example.com": {testUser, testPass}},
					[]string{"*.example.com"})
				provider, err := NewKubeletProvider(cfg, binDir)
				require.NoError(t, err)
				return provider
			},
			request:     &AuthRequest{Ref: ""},
			wantErr:     true,
			errContains: "ref not found",
		},
		{
			name: "successful credential retrieval",
			setup: func(t *testing.T) *KubeletProvider {
				cfg := filepath.Join(binDir, "success-config.yaml")
				setupMockProvider(t, binDir, "success-plugin", cfg,
					credentialMap{"registry.example.com": {testUser, testPass}},
					[]string{"*.example.com"})
				provider, err := NewKubeletProvider(cfg, binDir)
				require.NoError(t, err)
				return provider
			},
			request:      &AuthRequest{Ref: "registry.example.com/image:tag"},
			wantUsername: testUser,
			wantPassword: testPass,
		},
		{
			name: "no matching plugin",
			setup: func(t *testing.T) *KubeletProvider {
				cfg := filepath.Join(binDir, "nomatch-config.yaml")
				setupMockProvider(t, binDir, "nomatch-plugin", cfg,
					credentialMap{"registry.docker.io": {testUser, testPass}},
					[]string{"*.docker.io"})
				provider, err := NewKubeletProvider(cfg, binDir)
				require.NoError(t, err)
				return provider
			},
			request:     &AuthRequest{Ref: "example.com/image:tag"},
			wantNil:     true,
			wantErr:     true,
			errContains: "no credentials found",
		},
		{
			name: "plugin returns no auth",
			setup: func(t *testing.T) *KubeletProvider {
				cfg := filepath.Join(binDir, "noauth-config.yaml")
				setupMockProvider(t, binDir, "noauth-plugin", cfg, credentialMap{"registry.docker.io": {}}, []string{"*.example.com"})
				provider, err := NewKubeletProvider(cfg, binDir)
				require.NoError(t, err)
				return provider
			},
			request:     &AuthRequest{Ref: "registry.example.com/image:tag"},
			wantNil:     true,
			wantErr:     true,
			errContains: "no matching registries found",
		},
		{
			name: "multiple plugins credentials collected",
			setup: func(t *testing.T) *KubeletProvider {
				createMockPlugin(t, binDir, "first-plugin", credentialMap{"registry.example.com": {"first-user", "first-pass"}})
				createMockPlugin(t, binDir, "second-plugin", credentialMap{"registry.example.com": {"second-user", "second-pass"}})
				cfg := filepath.Join(binDir, "multi-config.yaml")
				createMockProviderConfig(t, cfg, []kubeletconfigv1.CredentialProvider{
					createMockCredentialProvider("first-plugin", []string{"*.example.com"}),
					createMockCredentialProvider("second-plugin", []string{"*.example.com"}),
				})
				provider, err := NewKubeletProvider(cfg, binDir)
				require.NoError(t, err)
				return provider
			},
			request:      &AuthRequest{Ref: "registry.example.com/image:tag"},
			wantUsername: "second-user",
			wantPassword: "second-pass",
		},
		{
			name: "most specific registry path wins - specific over general",
			setup: func(t *testing.T) *KubeletProvider {
				cfg := filepath.Join(binDir, "specificity-config.yaml")
				setupMockProvider(t, binDir, "specificity-plugin", cfg,
					credentialMap{
						"gcr.io":                    {"general-user", "general-pass"},
						"gcr.io/etcd-development":   {"specific-user", "specific-pass"},
						"gcr.io/kubernetes-release": {"k8s-user", "k8s-pass"},
					},
					[]string{"gcr.io", "gcr.io/*"})
				provider, err := NewKubeletProvider(cfg, binDir)
				require.NoError(t, err)
				return provider
			},
			request:      &AuthRequest{Ref: "gcr.io/etcd-development/etcd:v3.5.0"},
			wantUsername: "specific-user",
			wantPassword: "specific-pass",
		},
		{
			name: "most specific registry path wins - deeper path preferred",
			setup: func(t *testing.T) *KubeletProvider {
				cfg := filepath.Join(binDir, "deep-path-config.yaml")
				setupMockProvider(t, binDir, "deep-path-plugin", cfg,
					credentialMap{
						"registry.example.com":          {"level1-user", "level1-pass"},
						"registry.example.com/org":      {"level2-user", "level2-pass"},
						"registry.example.com/org/team": {"level3-user", "level3-pass"},
					},
					[]string{"registry.example.com"})
				provider, err := NewKubeletProvider(cfg, binDir)
				require.NoError(t, err)
				return provider
			},
			request:      &AuthRequest{Ref: "registry.example.com/org/team/app:latest"},
			wantUsername: "level3-user",
			wantPassword: "level3-pass",
		},
		{
			name: "alphabetical sorting ensures consistent specificity",
			setup: func(t *testing.T) *KubeletProvider {
				cfg := filepath.Join(binDir, "alpha-config.yaml")
				setupMockProvider(t, binDir, "alpha-plugin", cfg,
					credentialMap{
						"registry.example.com":         {"root-user", "root-pass"},
						"registry.example.com/aaa":     {"aaa-user", "aaa-pass"},
						"registry.example.com/aaa/bbb": {"nested-user", "nested-pass"},
					},
					[]string{"registry.example.com"})
				provider, err := NewKubeletProvider(cfg, binDir)
				require.NoError(t, err)
				return provider
			},
			request:      &AuthRequest{Ref: "registry.example.com/aaa/bbb/image:tag"},
			wantUsername: "nested-user",
			wantPassword: "nested-pass",
		},
		{
			name: "only first plugin matches - verifies no loop variable pointer bug",
			setup: func(t *testing.T) *KubeletProvider {
				// Create two plugins with different matchImages and credentials
				createMockPlugin(t, binDir, "first-match-plugin", credentialMap{"registry.first.com": {"first-user", "first-pass"}})
				createMockPlugin(t, binDir, "second-no-match-plugin", credentialMap{"registry.second.com": {"second-user", "second-pass"}})
				cfg := filepath.Join(binDir, "first-match-config.yaml")
				createMockProviderConfig(t, cfg, []kubeletconfigv1.CredentialProvider{
					createMockCredentialProvider("first-match-plugin", []string{"*.first.com"}),
					createMockCredentialProvider("second-no-match-plugin", []string{"*.second.com"}),
				})
				provider, err := NewKubeletProvider(cfg, binDir)
				require.NoError(t, err)
				return provider
			},
			request:      &AuthRequest{Ref: "registry.first.com/image:tag"},
			wantUsername: "first-user",
			wantPassword: "first-pass",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := tt.setup(t)
			kubeletProvider = provider

			kc, err := provider.GetCredentials(tt.request)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, kc)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, kc)
				return
			}

			require.NotNil(t, kc)
			assert.Equal(t, tt.wantUsername, kc.Username)
			assert.Equal(t, tt.wantPassword, kc.Password)
		})
	}
}

func TestInitKubeletProvider(t *testing.T) {
	// Save and restore the global kubeletProvider
	oldProvider := kubeletProvider
	defer func() { kubeletProvider = oldProvider }()

	configPath, binDir := setupKubeletProvider(t)

	tests := []struct {
		name        string
		setup       func(t *testing.T) (configPath, binDir string)
		wantErr     bool
		errContains string
		validate    func(t *testing.T)
	}{
		{
			name: "successful initialization",
			setup: func(t *testing.T) (string, string) {
				kubeletProvider = nil
				setupMockProvider(t, binDir, "init-plugin", configPath,
					credentialMap{"registry.example.com": {testUser, testPass}},
					[]string{"*.example.com"})
				return configPath, binDir
			},
			validate: func(t *testing.T) {
				assert.NotNil(t, kubeletProvider)
			},
		},
		{
			name: "idempotent initialization",
			setup: func(t *testing.T) (string, string) {
				// Provider already initialized from previous test
				return configPath, binDir
			},
			wantErr: false,
			validate: func(t *testing.T) {
				assert.NotNil(t, kubeletProvider)
			},
		},
		{
			name: "initialization with invalid config",
			setup: func(t *testing.T) (string, string) {
				kubeletProvider = nil
				return "/nonexistent/config.yaml", binDir
			},
			wantErr: true,
			validate: func(t *testing.T) {
				assert.Nil(t, kubeletProvider)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, binDir := tt.setup(t)
			err := InitKubeletProvider(configPath, binDir)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}

			if tt.validate != nil {
				tt.validate(t)
			}
		})
	}
}

func TestURLsMatch(t *testing.T) {
	tests := []struct {
		name      string
		globURL   string
		targetURL string
		wantMatch bool
		wantError bool
	}{
		{
			name:      "exact match",
			globURL:   "example.com",
			targetURL: "example.com",
			wantMatch: true,
		},
		{
			name:      "wildcard subdomain match",
			globURL:   "*.docker.io",
			targetURL: "registry.docker.io",
			wantMatch: true,
		},
		{
			name:      "wildcard subdomain no match",
			globURL:   "*.docker.io",
			targetURL: "example.com",
			wantMatch: false,
		},
		{
			name:      "port mismatch",
			globURL:   "example.com:443",
			targetURL: "example.com:8080",
			wantMatch: false,
		},
		{
			name:      "port match",
			globURL:   "example.com:443",
			targetURL: "example.com:443",
			wantMatch: true,
		},
		{
			name:      "path prefix match",
			globURL:   "example.com/prefix",
			targetURL: "example.com/prefix/image",
			wantMatch: true,
		},
		{
			name:      "path prefix no match",
			globURL:   "example.com/prefix",
			targetURL: "example.com/other",
			wantMatch: false,
		},
		{
			name:      "different number of parts",
			globURL:   "*.example.com",
			targetURL: "example.com",
			wantMatch: false,
		},
		{
			name:      "malformed glob URL with invalid escape",
			globURL:   "example.com%",
			targetURL: "example.com",
			wantMatch: false,
			wantError: true,
		},
		{
			name:      "malformed target URL with invalid escape",
			globURL:   "example.com",
			targetURL: "example.com%",
			wantMatch: false,
			wantError: true,
		},
		{
			name:      "invalid glob pattern unclosed bracket",
			globURL:   "[.example.com",
			targetURL: "x.example.com",
			wantMatch: false,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, err := urlsMatchStr(tt.globURL, tt.targetURL)
			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantMatch, matched)
		})
	}
}

// --- evictExpired ---

func TestKubeletProviderEvictExpired(t *testing.T) {
	now := time.Now()
	provider := &KubeletProvider{
		cache: map[string]*kubeletCredential{
			"valid.com":   {keychains: map[string]*PassKeyChain{"valid.com": {}}, expiresAt: now.Add(10 * time.Minute)},
			"expired.com": {keychains: map[string]*PassKeyChain{"expired.com": {}}, expiresAt: now.Add(-1 * time.Minute)},
			"zero.com":    {keychains: map[string]*PassKeyChain{"zero.com": {}}, expiresAt: time.Time{}},
		},
	}

	provider.evictExpired()

	assert.Len(t, provider.cache, 1)
	assert.Contains(t, provider.cache, "valid.com")
	assert.NotContains(t, provider.cache, "expired.com")
	assert.NotContains(t, provider.cache, "zero.com")
}

// TestKubeletProviderValidUntil verifies that ValidUntil causes the provider to
// bypass a cached credential that won't survive until the requested time, while
// still serving from cache when the cached credential is valid long enough.
func TestKubeletProviderValidUntil(t *testing.T) {
	_, binDir := setupKubeletProvider(t)

	pluginName := "valid-until-plugin"
	cfgPath := filepath.Join(binDir, "valid-until-config.yaml")
	pluginPath := filepath.Join(binDir, pluginName)

	// Plugin returns a 10-minute TTL; cached expiresAt ≈ now+10m.
	createMockPluginWithTTL(t, binDir, pluginName,
		credentialMap{"registry.example.com": {testUser, testPass}}, "10m")
	createMockProviderConfig(t, cfgPath, []kubeletconfigv1.CredentialProvider{
		createMockCredentialProvider(pluginName, []string{"registry.example.com"}),
	})

	provider, err := NewKubeletProvider(cfgPath, binDir)
	require.NoError(t, err)

	ref := "registry.example.com/image:tag"

	// First call: executes the plugin and caches the result (expiresAt ≈ now+10m).
	kc, err := provider.GetCredentials(&AuthRequest{Ref: ref})
	require.NoError(t, err)
	require.NotNil(t, kc)
	assert.Equal(t, testUser, kc.Username)

	// Remove the plugin binary; any re-execution attempt will now fail.
	require.NoError(t, os.Remove(pluginPath))

	// ValidUntil within the cached TTL (now+5m < expiresAt ≈ now+10m): cache hit.
	kc2, err := provider.GetCredentials(&AuthRequest{Ref: ref, ValidUntil: time.Now().Add(5 * time.Minute)})
	require.NoError(t, err, "credential is valid long enough; expected cache hit")
	require.NotNil(t, kc2)
	assert.Equal(t, testUser, kc2.Username)

	// ValidUntil beyond the cached TTL (now+20m > expiresAt ≈ now+10m): cache
	// bypassed, re-exec attempted → fails because the binary is gone.
	_, err = provider.GetCredentials(&AuthRequest{Ref: ref, ValidUntil: time.Now().Add(20 * time.Minute)})
	require.Error(t, err, "cached credential won't last long enough; expected plugin re-exec and failure")

	// Cache is still intact (failed re-exec didn't evict it): zero ValidUntil succeeds.
	kc3, err := provider.GetCredentials(&AuthRequest{Ref: ref})
	require.NoError(t, err, "cache should be unaffected by the failed re-exec attempt")
	require.NotNil(t, kc3)
	assert.Equal(t, testUser, kc3.Username)
}

// TestKubeletProviderRegistryCache verifies caching behaviour by removing the
// plugin binary after the first successful call. The second call either
// succeeds (cache hit) or fails (no cache), proving whether the result was
// stored.
func TestKubeletProviderRegistryCache(t *testing.T) {
	tests := []struct {
		name             string
		pluginName       string
		cfgName          string
		responseTTL      string        // cacheDuration field in the plugin response
		defaultTTL       time.Duration // DefaultCacheDuration in the provider config
		wantCachedOnNext bool          // whether second call should succeed from cache
	}{
		{
			name:             "positive TTL caches result",
			pluginName:       "cache-hit-plugin",
			cfgName:          "cache-hit-config.yaml",
			responseTTL:      "10m",
			defaultTTL:       10 * time.Minute,
			wantCachedOnNext: true,
		},
		{
			name:             "zero TTL does not cache",
			pluginName:       "zero-ttl-plugin",
			cfgName:          "zero-ttl-config.yaml",
			responseTTL:      "0s",
			defaultTTL:       0,
			wantCachedOnNext: false,
		},
	}

	_, binDir := setupKubeletProvider(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pluginPath := filepath.Join(binDir, tt.pluginName)
			cfg := filepath.Join(binDir, tt.cfgName)

			createMockPluginWithTTL(t, binDir, tt.pluginName,
				credentialMap{"registry.example.com": {testUser, testPass}}, tt.responseTTL)
			createMockProviderConfig(t, cfg, []kubeletconfigv1.CredentialProvider{
				createMockCredentialProviderWithTTL(tt.pluginName, []string{"registry.example.com"}, tt.defaultTTL),
			})

			provider, err := NewKubeletProvider(cfg, binDir)
			require.NoError(t, err)

			// First call: executes the plugin.
			kc, err := provider.GetCredentials(&AuthRequest{Ref: "registry.example.com/image:tag"})
			require.NoError(t, err)
			require.NotNil(t, kc)
			assert.Equal(t, testUser, kc.Username)

			// Remove the plugin so any subsequent exec attempt fails.
			require.NoError(t, os.Remove(pluginPath))

			// Second call: served from cache (hit) or re-executes the gone plugin (miss).
			kc2, err := provider.GetCredentials(&AuthRequest{Ref: "registry.example.com/image:tag"})
			if tt.wantCachedOnNext {
				require.NoError(t, err, "expected cache hit; plugin binary is gone")
				require.NotNil(t, kc2)
				assert.Equal(t, testUser, kc2.Username)
			} else {
				require.Error(t, err, "expected miss: result should not have been cached")
			}
		})
	}
}

// TestKubeletProviderCacheKeyType verifies that the cache key is determined by
// the cacheKeyType field in the plugin response, following the kubelet behavior.
func TestKubeletProviderCacheKeyType(t *testing.T) {
	_, binDir := setupKubeletProvider(t)

	tests := []struct {
		name         string
		cacheKeyType string
		// firstRef is the image used for the first call (populates the cache).
		firstRef string
		// secondRef is the image used for the second call (after plugin removal).
		// Must share the same registry or be anything for Global.
		secondRef string
		// wantCacheHit indicates whether the second call should succeed from cache.
		wantCacheHit bool
	}{
		{
			name:         "Image: same image hits cache",
			cacheKeyType: "Image",
			firstRef:     "registry.example.com/image:tag",
			secondRef:    "registry.example.com/image:tag",
			wantCacheHit: true,
		},
		{
			name:         "Image: different image misses cache",
			cacheKeyType: "Image",
			firstRef:     "registry.example.com/image-a:tag",
			secondRef:    "registry.example.com/image-b:tag",
			wantCacheHit: false,
		},
		{
			name:         "Registry: same registry different image hits cache",
			cacheKeyType: "Registry",
			firstRef:     "registry.example.com/image-a:tag",
			secondRef:    "registry.example.com/image-b:tag",
			wantCacheHit: true,
		},
		{
			name:         "Registry: different registry misses cache",
			cacheKeyType: "Registry",
			firstRef:     "registry.example.com/image:tag",
			secondRef:    "other.example.com/image:tag",
			wantCacheHit: false,
		},
		{
			name:         "Global: any image hits cache",
			cacheKeyType: "Global",
			firstRef:     "registry.example.com/image:tag",
			secondRef:    "other.example.com/different:tag",
			wantCacheHit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pluginName := "ckt-" + strings.ReplaceAll(strings.ToLower(tt.name[:10]), " ", "-")
			cfgName := pluginName + "-config.yaml"

			createMockPluginFull(t, binDir, pluginName,
				credentialMap{
					"registry.example.com": {testUser, testPass},
					"other.example.com":    {testUser, testPass},
				}, "10m", tt.cacheKeyType)
			cfg := filepath.Join(binDir, cfgName)
			createMockProviderConfig(t, cfg, []kubeletconfigv1.CredentialProvider{
				createMockCredentialProviderWithTTL(pluginName,
					[]string{"*.example.com"}, 10*time.Minute),
			})

			provider, err := NewKubeletProvider(cfg, binDir)
			require.NoError(t, err)

			// First call: execute the plugin and populate the cache.
			kc, err := provider.GetCredentials(&AuthRequest{Ref: tt.firstRef})
			require.NoError(t, err)
			require.NotNil(t, kc)

			// Remove the plugin binary so any re-exec attempt fails.
			require.NoError(t, os.Remove(filepath.Join(binDir, pluginName)))

			// Second call: should hit or miss the cache depending on cacheKeyType.
			kc2, err := provider.GetCredentials(&AuthRequest{Ref: tt.secondRef})
			if tt.wantCacheHit {
				require.NoError(t, err, "expected cache hit")
				require.NotNil(t, kc2)
				assert.Equal(t, testUser, kc2.Username)
			} else {
				require.Error(t, err, "expected cache miss")
			}
		})
	}
}

// --- computeCacheKey ---

func TestComputeCacheKey(t *testing.T) {
	tests := []struct {
		name         string
		cacheKeyType credentialproviderv1.PluginCacheKeyType
		image        string
		want         string
	}{
		{
			name:         "Image returns full image",
			cacheKeyType: credentialproviderv1.ImagePluginCacheKeyType,
			image:        "registry.example.com/org/app:latest",
			want:         "registry.example.com/org/app:latest",
		},
		{
			name:         "Registry returns host only",
			cacheKeyType: credentialproviderv1.RegistryPluginCacheKeyType,
			image:        "registry.example.com/org/app:latest",
			want:         "registry.example.com",
		},
		{
			name:         "Global returns global key",
			cacheKeyType: credentialproviderv1.GlobalPluginCacheKeyType,
			image:        "registry.example.com/org/app:latest",
			want:         "<global>",
		},
		{
			name:         "unknown returns empty",
			cacheKeyType: "Unknown",
			image:        "registry.example.com/org/app:latest",
			want:         "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeCacheKey(tt.cacheKeyType, tt.image)
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- parseRegistry ---

func TestParseRegistry(t *testing.T) {
	tests := []struct {
		image string
		want  string
	}{
		{"registry.example.com/org/app:latest", "registry.example.com"},
		{"localhost:5000/myimage", "localhost:5000"},
		{"docker.io/library/nginx:latest", "docker.io"},
		{"singlepart", "singlepart"},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			assert.Equal(t, tt.want, parseRegistry(tt.image))
		})
	}
}
