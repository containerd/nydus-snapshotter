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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeletconfigv1 "k8s.io/kubelet/config/v1"
	"sigs.k8s.io/yaml"
)

const (
	testUser = "test-user"
	testPass = "test-pass"
)

type credentialMap map[string]struct{ username, password string }

// setupKubeletProvider creates a temporary directory with mock credential provider
// plugins and configuration file for testing.
func setupKubeletProvider(t *testing.T) (configPath, binDir string, cleanup func()) {
	t.Helper()

	// Create temporary directories
	tmpDir, err := os.MkdirTemp("", "kubelet-test-")
	require.NoError(t, err)

	binDir = filepath.Join(tmpDir, "bin")
	err = os.Mkdir(binDir, 0755)
	require.NoError(t, err)

	configPath = filepath.Join(tmpDir, "credential-config.yaml")

	cleanup = func() {
		os.RemoveAll(tmpDir)
	}

	return configPath, binDir, cleanup
}

// createMockPlugin creates a mock credential provider plugin that returns
// credentials based on the input image. If registries is empty, return null auth.
func createMockPlugin(t *testing.T, binDir, pluginName string, registries credentialMap) {
	t.Helper()

	pluginPath := filepath.Join(binDir, pluginName)

	// Build the auth section
	authEntries := "null"
	if len(registries) > 0 {
		authEntries = "{"
		first := true
		for registry, creds := range registries {
			if !first {
				authEntries += ","
			}
			authEntries += fmt.Sprintf(`"%s":{"username":"%s","password":"%s"}`,
				registry, creds.username, creds.password)
			first = false
		}
		authEntries += "}"
	}

	script := fmt.Sprintf(`#!/bin/bash
cat > /dev/null
cat <<'EOF'
{"kind":"CredentialProviderResponse","apiVersion":"credentialprovider.kubelet.k8s.io/v1","cacheKeyType":"Image","cacheDuration":"10m","auth":%s}
EOF
`, authEntries)

	err := os.WriteFile(pluginPath, []byte(script), 0755)
	require.NoError(t, err)
}

// createMockCredentialProvider creates a CredentialProvider with the given parameters.
func createMockCredentialProvider(name string, matchImages []string) kubeletconfigv1.CredentialProvider {
	return kubeletconfigv1.CredentialProvider{
		Name:                 name,
		APIVersion:           "credentialprovider.kubelet.k8s.io/v1",
		DefaultCacheDuration: &metav1.Duration{Duration: 10 * time.Minute},
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
	configPath, binDir, cleanup := setupKubeletProvider(t)
	defer cleanup()

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
			errContains: "cannot contain path separators",
		},
		{
			name: "provider name with space",
			setup: func() *kubeletconfigv1.CredentialProvider {
				provider := createMockCredentialProvider("valid-plugin", []string{"*.example.com"})
				provider.Name = "invalid name"
				return &provider
			},
			wantErr:     true,
			errContains: "cannot contain path separators",
		},
		{
			name: "provider name is dot",
			setup: func() *kubeletconfigv1.CredentialProvider {
				provider := createMockCredentialProvider("valid-plugin", []string{"*.example.com"})
				provider.Name = "."
				return &provider
			},
			wantErr:     true,
			errContains: "cannot contain path separators",
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

	configPath, binDir, cleanup := setupKubeletProvider(t)
	defer cleanup()

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

	configPath, binDir, cleanup := setupKubeletProvider(t)
	defer cleanup()

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
			matched, err := URLsMatchStr(tt.globURL, tt.targetURL)
			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantMatch, matched)
		})
	}
}
