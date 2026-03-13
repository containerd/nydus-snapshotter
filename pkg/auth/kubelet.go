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
	plugins    []*kubeletconfigv1.CredentialProvider
	binDir     string
	mu         sync.RWMutex
	registries map[string]*kubeletCredential // registry glob -> cached credential
}

// kubeletCredential pairs a PassKeyChain with its provider-reported expiry.
type kubeletCredential struct {
	keychain  *PassKeyChain
	expiresAt time.Time // zero means no cache (always re-exec plugin)
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
		plugins:    make([]*kubeletconfigv1.CredentialProvider, 0, len(config.Providers)),
		binDir:     binDir,
		registries: make(map[string]*kubeletCredential),
	}

	// Validate and register credential providers
	// Heavily inspired by kubelet's validateCredentialProviderConfig function
	credProviderNames := make(map[string]struct{})
	for i := range config.Providers {
		if err := validateCredentialProvider(&config.Providers[i]); err != nil {
			return nil, errors.Wrapf(err, "failed to validate credential provider %s", config.Providers[i].Name)
		}

		// Check for duplicate provider names
		if _, ok := credProviderNames[config.Providers[i].Name]; ok {
			return nil, fmt.Errorf("duplicate provider name: %s", config.Providers[i].Name)
		}
		credProviderNames[config.Providers[i].Name] = struct{}{}
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
	envNames := make(map[string]struct{})
	for _, env := range p.Env {
		if env.Name == "" {
			return fmt.Errorf("provider %s: environment variable name cannot be empty", p.Name)
		}
		if _, ok := envNames[env.Name]; ok {
			return fmt.Errorf("provider %s: duplicate environment variable name: %s", p.Name, env.Name)
		}
		envNames[env.Name] = struct{}{}
	}

	return nil
}

// CanRenew implements RenewableProvider. Kubelet credentials can be
// refreshed by re-executing the credential provider plugins.
func (p *KubeletProvider) CanRenew() bool { return true }

func (p *KubeletProvider) String() string {
	return "kubelet"
}

// GetCredentials retrieves credentials using kubelet credential provider plugins.
// It first checks the internal registry cache for a non-expired match (using the
// same wildcard matching logic as the kubelet). On a cache miss it executes all
// matching plugins, stores every returned registry entry in the cache, and then
// returns the most specific match for the requested ref.
func (p *KubeletProvider) GetCredentials(req *AuthRequest) (*PassKeyChain, error) {
	if req == nil || req.Ref == "" {
		return nil, errors.New("ref not found in request")
	}

	// Normalize the reference to handle short refs like "nginx:latest" -> "docker.io/library/nginx:latest"
	refSpec, _, err := parseReference(req.Ref)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse image reference")
	}

	// Evict expired before adding new ones to bound the map size
	p.evictExpired()

	// Fast path: serve from the registry cache when a valid-long-enough credential
	// exists. Otherwise, re-execute the plugin.
	cred := p.bestMatchedCred(refSpec.String())
	if cred != nil && (req.ValidUntil.IsZero() || cred.expiresAt.After(req.ValidUntil)) {
		log.L.WithField("ref", req.Ref).Debug("serving kubelet credentials from registry cache")
		return cred.keychain, nil
	}

	// Slow path: execute matching plugins
	allCredentials := make(map[string]*kubeletCredential)
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
			log.L.WithError(err).WithFields(map[string]any{"plugin": plugin.Name, "ref": req.Ref}).Warn("failed to execute credential provider plugin")
			continue
		}

		if resp.Auth == nil {
			// Spec: A plugin should set this field (Auth) to null if no valid credentials can be returned for the requested image.
			continue
		}

		var expiresAt time.Time
		if d := resolveCacheDuration(resp, plugin); d > 0 {
			expiresAt = time.Now().Add(d)
		}

		for registry, authConfig := range resp.Auth {
			c := &kubeletCredential{
				keychain: &PassKeyChain{
					Username: authConfig.Username,
					Password: authConfig.Password,
				},
				expiresAt: expiresAt,
			}
			allCredentials[registry] = c
			// Only cache when the plugin provides a TTL
			// A zero duration means no caching (plugin will be re-executed on the next request).
			if !expiresAt.IsZero() {
				p.mu.Lock()
				p.registries[registry] = c
				p.mu.Unlock()
			}
		}
	}

	if len(allCredentials) == 0 {
		return nil, errors.New("no credentials found")
	}

	cred = bestMatch(allCredentials, refSpec.String())
	if cred == nil {
		return nil, errors.New("no matching registries found")
	}
	return cred.keychain, nil
}

// evictExpired removes all expired entries from p.registries.
func (p *KubeletProvider) evictExpired() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for registry, cred := range p.registries {
		if cred.expiresAt.IsZero() || now.After(cred.expiresAt) {
			delete(p.registries, registry)
		}
	}
}

// bestMatch returns the most specific entry in m that matches image,
// using reverse-alphabetical order on the registry glob keys.
func bestMatch(m map[string]*kubeletCredential, image string) *kubeletCredential {
	var matching []string
	for registry := range m {
		if matched, err := urlsMatchStr(registry, image); err == nil && matched {
			matching = append(matching, registry)
		} else if err != nil {
			log.L.WithError(err).
				WithField("registry", registry).
				WithField("image", image).
				Warn("registry pattern does not match image")
		}
	}
	if len(matching) == 0 {
		return nil
	}
	// Sort in reverse alphabetical order: longer/more specific paths sort first
	// For example, "gcr.io/etcd-development" matches before "gcr.io".
	sort.Sort(sort.Reverse(sort.StringSlice(matching)))
	return m[matching[0]]
}

// bestMatchedCred returns the non-expired cached credential whose registry glob
// most specifically matches image. Returns nil when no valid match exists.
func (p *KubeletProvider) bestMatchedCred(image string) *kubeletCredential {
	p.mu.RLock()
	defer p.mu.RUnlock()
	now := time.Now()
	// Build a view of only the non-expired entries, then delegate the
	// match/sort logic to bestMatch.
	valid := make(map[string]*kubeletCredential, len(p.registries))
	for registry, cred := range p.registries {
		if !cred.expiresAt.IsZero() && !now.After(cred.expiresAt) {
			valid[registry] = cred
		}
	}
	return bestMatch(valid, image)
}

// resolveCacheDuration picks the effective TTL from a plugin response:
// the per-response CacheDuration takes precedence if it is positive,
// otherwise the plugin's DefaultCacheDuration is used.
func resolveCacheDuration(resp *credentialproviderv1.CredentialProviderResponse, plugin *kubeletconfigv1.CredentialProvider) time.Duration {
	if resp.CacheDuration != nil && resp.CacheDuration.Duration > 0 {
		return resp.CacheDuration.Duration
	}
	return plugin.DefaultCacheDuration.Duration
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
