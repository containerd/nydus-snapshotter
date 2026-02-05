/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/containerd/log"
	"github.com/pkg/errors"
	kubeletconfigv1 "k8s.io/kubelet/config/v1"
	credentialproviderv1 "k8s.io/kubelet/pkg/apis/credentialprovider/v1"
	"sigs.k8s.io/yaml"
)

const (
	pluginExecTimeout = 1 * time.Minute
)

var (
	kubeletProvider   *KubeletProvider
	kubeletProviderMu sync.Mutex
)

// KubeletProvider retrieves credentials using Kubernetes credential provider plugins.
type KubeletProvider struct {
	plugins []*kubeletconfigv1.CredentialProvider
	binDir  string
}

// InitKubeletProvider initializes the global kubelet credential provider.
// This should be called once at startup if kubelet credential providers are enabled.
func InitKubeletProvider(configPath, binDir string) error {
	kubeletProviderMu.Lock()
	defer kubeletProviderMu.Unlock()

	if kubeletProvider != nil {
		return nil
	}

	provider, err := NewKubeletProvider(configPath, binDir)
	if err != nil {
		return errors.Wrap(err, "failed to create kubelet provider")
	}

	kubeletProvider = provider
	log.L.Info("kubelet credential provider initialized")
	return nil
}

// NewKubeletProvider creates a new kubelet credential helpers-based auth provider.
func NewKubeletProvider(configPath, binDir string) (*KubeletProvider, error) {
	if configPath == "" {
		return nil, errors.New("config path cannot be empty")
	}

	if binDir == "" {
		return nil, errors.New("bin directory cannot be empty")
	}

	// Load configuration
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read config file %s", configPath)
	}

	var config kubeletconfigv1.CredentialProviderConfig
	// Using the yaml library from sigs.k8s.io/yaml because it supports both YAML and JSON
	// and the type CredentialProvider only has json markups which are used by this lib's Unmarshal
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, errors.Wrap(err, "failed to parse credential provider config")
	}

	if len(config.Providers) == 0 {
		return nil, errors.New("at least one provider is required")
	}

	provider := &KubeletProvider{
		plugins: make([]*kubeletconfigv1.CredentialProvider, 0, len(config.Providers)),
		binDir:  binDir,
	}

	// Validate and register credential providers
	// Heavily inspired by kubelet's validateCredentialProviderConfig function
	credProviderNames := make(map[string]any)
	for i := range config.Providers {
		if err := validateCredentialProvider(&config.Providers[i]); err != nil {
			return nil, errors.Wrapf(err, "failed to validate credential provider %s", config.Providers[i].Name)
		}

		// Check for duplicate provider names
		if _, ok := credProviderNames[config.Providers[i].Name]; ok {
			return nil, fmt.Errorf("duplicate provider name: %s", config.Providers[i].Name)
		}
		credProviderNames[config.Providers[i].Name] = nil
		provider.plugins = append(provider.plugins, &config.Providers[i])
		log.L.WithField("name", config.Providers[i].Name).Info("registered kubelet credential provider plugin")
	}

	return provider, nil
}

// validateCredentialProvider validates a single credential provider configuration.
func validateCredentialProvider(p *kubeletconfigv1.CredentialProvider) error {
	// Validate provider name
	if p.Name == "" {
		return fmt.Errorf("provider name is required")
	}

	// Provider name must not contain path separators or special characters
	if strings.ContainsAny(p.Name, " /\\") || p.Name == "." || p.Name == ".." {
		return fmt.Errorf("invalid provider name %q: cannot contain spaces, path separators, or be '.' or '..'", p.Name)
	}

	// Validate API version
	if p.APIVersion == "" {
		return fmt.Errorf("provider %s: apiVersion is required", p.Name)
	}

	// Only support v1 for now
	if p.APIVersion != "credentialprovider.kubelet.k8s.io/v1" {
		return fmt.Errorf("provider %s: unsupported apiVersion %q (only v1 is supported)", p.Name, p.APIVersion)
	}

	// Validate match images
	if len(p.MatchImages) == 0 {
		return fmt.Errorf("provider %s: at least one matchImage is required", p.Name)
	}

	if slices.Contains(p.MatchImages, "") {
		return fmt.Errorf("provider %s: empty matchImage pattern", p.Name)
	}

	// Validate cache duration
	if p.DefaultCacheDuration == nil {
		return fmt.Errorf("provider %s: defaultCacheDuration is required", p.Name)
	}

	if p.DefaultCacheDuration.Duration < 0 {
		return fmt.Errorf("provider %s: defaultCacheDuration must be >= 0", p.Name)
	}

	// Validate environment variables
	envNames := make(map[string]bool)
	for _, env := range p.Env {
		if env.Name == "" {
			return fmt.Errorf("provider %s: environment variable name cannot be empty", p.Name)
		}
		if envNames[env.Name] {
			return fmt.Errorf("provider %s: duplicate environment variable name: %s", p.Name, env.Name)
		}
		envNames[env.Name] = true
	}

	return nil
}

// GetCredentials retrieves credentials using kubelet credential provider plugins.
// When multiple credentials are available, it returns the one with the most specific
// registry path match (e.g., "gcr.io/etcd-development" before "gcr.io").
func (p *KubeletProvider) GetCredentials(req *AuthRequest) (*PassKeyChain, error) {
	if req == nil || req.Ref == "" {
		return nil, errors.New("ref not found in request")
	}

	// Normalize the reference to handle short refs like "nginx:latest" -> "docker.io/library/nginx:latest"
	refSpec, _, err := parseReference(req.Ref)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse image reference")
	}

	// Collect all available credentials from all matching plugins
	allCredentials := make(map[string]*PassKeyChain)

	for _, plugin := range p.plugins {
		if !isImageAllowed(plugin, refSpec.String()) {
			continue
		}

		// The spec mentions that:
		// "If one of the strings matches the requested image from the kubelet, the plugin will be invoked and given a chance to provide credentials"
		// So each matching plugin should have a chance to fetch credentials
		// TODO: parallelize?
		resp, err := p.execPlugin(context.Background(), plugin, refSpec.String())
		if err != nil {
			log.L.WithError(err).WithField("plugin", plugin.Name).Warn("failed to execute credential provider plugin")
			continue
		}

		if resp.Auth == nil {
			// Spec: A plugin should set this field (Auth) to null if no valid credentials can be returned for the requested image.
			continue
		}

		// Collect all credentials from this plugin
		for registry, authConfig := range resp.Auth {
			allCredentials[registry] = &PassKeyChain{
				Username: authConfig.Username,
				Password: authConfig.Password,
			}
		}
	}

	if len(allCredentials) == 0 {
		return nil, errors.New("no credentials found")
	}

	// Filter to only registries that match the requested image.
	// Then sort by specificity (reverse alphabetical order) to ensure
	// more specific paths are matched first.
	// For example, "gcr.io/etcd-development" matches before "gcr.io".
	matchingRegistries := make([]string, 0, len(allCredentials))
	for registry := range allCredentials {
		// Check if this registry key matches the requested image
		matched, err := urlsMatchStr(registry, refSpec.String())
		if err == nil && matched {
			matchingRegistries = append(matchingRegistries, registry)
		}
	}

	log.L.Debugf("Total credentials: %d, Matching registries: %d", len(allCredentials), len(matchingRegistries))

	if len(matchingRegistries) == 0 {
		return nil, errors.New("no matching registries found")
	}

	// Sort in reverse alphabetical order - longer/more specific paths sort first
	sort.Sort(sort.Reverse(sort.StringSlice(matchingRegistries)))
	log.L.Debugf("Selected registry after sorting: %s", matchingRegistries[0])

	// Return the credential with the most specific match
	return allCredentials[matchingRegistries[0]], nil
}

// isImageAllowed returns true if the image matches against the list of allowed matches by the plugin.
// Inspired from https://github.com/kubernetes/kubernetes/blob/v1.35.0/pkg/credentialprovider/plugin/plugin.go#L603
func isImageAllowed(plugin *kubeletconfigv1.CredentialProvider, image string) bool {
	for _, matchImage := range plugin.MatchImages {
		matched, err := urlsMatchStr(matchImage, image)
		if err != nil {
			log.L.WithError(err).
				WithField("plugin", plugin.Name).
				WithField("matchImage", matchImage).
				Warn("invalid matchImage pattern in credential provider config")
			continue
		}
		if matched {
			return true
		}
	}

	return false
}

// execPlugin executes the credential provider plugin binary.
func (p *KubeletProvider) execPlugin(ctx context.Context, plugin *kubeletconfigv1.CredentialProvider, image string) (*credentialproviderv1.CredentialProviderResponse, error) {
	request := &credentialproviderv1.CredentialProviderRequest{
		Image: image,
	}
	request.APIVersion = plugin.APIVersion
	request.Kind = "CredentialProviderRequest"

	// Encode request
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal request")
	}

	// Create command with timeout
	ctx, cancel := context.WithTimeout(ctx, pluginExecTimeout)
	defer cancel()

	// Inherit environment from snapshotter
	env := os.Environ()
	for _, e := range plugin.Env {
		env = append(env, fmt.Sprintf("%s=%s", e.Name, e.Value))
	}

	cmd := exec.CommandContext(ctx, filepath.Join(p.binDir, plugin.Name), plugin.Args...)
	cmd.Env = env

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	stdin := bytes.NewBuffer(requestJSON)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = stdout, stderr, stdin

	// Execute
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil, errors.Wrap(ctx.Err(), "error execing credential provider plugin")
	}
	if err != nil {
		stderrStr := stderr.String()
		return nil, errors.Wrapf(err, "error execing credential provider plugin, stderr: %s", stderrStr)
	}

	// Decode response
	var response credentialproviderv1.CredentialProviderResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return nil, errors.Wrap(err, "failed to decode plugin response")
	}

	if response.APIVersion != request.APIVersion {
		return nil, fmt.Errorf("plugin returned API version %s, expected %s",
			response.APIVersion, request.APIVersion)
	}

	return &response, nil
}

/*
#####								PORTED FROM KUBERNETES								#####

The following functions are ported from k8s.io/kubernetes/pkg/credentialprovider/keyring.go
Since they are in `k8s.io/kubernetes`, we can't import them directly.
*/

// parseSchemelessURL parses a schemeless url and returns a url.URL
// url.Parse require a scheme, but ours don't have schemes.  Adding a
// scheme to make url.Parse happy, then clear out the resulting scheme.
// Ported: https://github.com/kubernetes/kubernetes/blob/v1.35.0/pkg/credentialprovider/keyring.go#L220
func parseSchemelessURL(schemelessURL string) (*url.URL, error) {
	parsed, err := url.Parse("https://" + schemelessURL)
	if err != nil {
		return nil, err
	}
	// clear out the resulting scheme
	parsed.Scheme = ""
	return parsed, nil
}

// splitURL splits the host name into parts, as well as the port
// Ported: https://github.com/kubernetes/kubernetes/blob/v1.35.0/pkg/credentialprovider/keyring.go#L231
func splitURL(url *url.URL) (parts []string, port string) {
	host, port, err := net.SplitHostPort(url.Host)
	if err != nil {
		// could not parse port
		host, port = url.Host, ""
	}
	return strings.Split(host, "."), port
}

// urlsMatchStr is wrapper for urlsMatch, operating on strings instead of URLs.
// Ported: https://github.com/kubernetes/kubernetes/blob/v1.35.0/pkg/credentialprovider/keyring.go#L241
func urlsMatchStr(glob string, target string) (bool, error) {
	globURL, err := parseSchemelessURL(glob)
	if err != nil {
		return false, err
	}
	targetURL, err := parseSchemelessURL(target)
	if err != nil {
		return false, err
	}
	return urlsMatch(globURL, targetURL)
}

// urlsMatch checks whether the given target url matches the glob url, which may have
// glob wild cards in the host name.
//
// Examples:
//
//	globURL=*.docker.io, targetURL=blah.docker.io => match
//	globURL=*.docker.io, targetURL=not.right.io   => no match
//
// Note that we don't support wildcards in ports and paths yet.
// Ported: https://github.com/kubernetes/kubernetes/blob/v1.35.0/pkg/credentialprovider/keyring.go#L262
func urlsMatch(globURL *url.URL, targetURL *url.URL) (bool, error) {
	globURLParts, globPort := splitURL(globURL)
	targetURLParts, targetPort := splitURL(targetURL)
	if globPort != targetPort {
		// port doesn't match
		return false, nil
	}
	if len(globURLParts) != len(targetURLParts) {
		// host name does not have the same number of parts
		return false, nil
	}
	if !strings.HasPrefix(targetURL.Path, globURL.Path) {
		// the path of the credential must be a prefix
		return false, nil
	}
	for k, globURLPart := range globURLParts {
		targetURLPart := targetURLParts[k]
		matched, err := filepath.Match(globURLPart, targetURLPart)
		if err != nil {
			return false, err
		}
		if !matched {
			// glob mismatch for some part
			return false, nil
		}
	}
	// everything matches
	return true, nil
}

/*
#####						END OF PORTED FROM KUBERNETES						#####
*/
