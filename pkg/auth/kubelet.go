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
	"maps"
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
	// globalCacheKey is the key used for caching credentials that are not specific to a registry or image.
	// Angle brackets are used to avoid conflicts with actual image or registry names.
	globalCacheKey = "<global>"
)

var (
	kubeletProvider   *KubeletProvider
	kubeletProviderMu sync.Mutex
)

// KubeletProvider retrieves credentials using Kubernetes credential provider plugins.
type KubeletProvider struct {
	plugins []*kubeletconfigv1.CredentialProvider
	binDir  string
	mu      sync.RWMutex
	cache   map[string]*kubeletCredential // cache key (image or registry or global) -> cached credential
}

// kubeletCredential pairs a PassKeyChain with its provider-reported expiry.
type kubeletCredential struct {
	keychains map[string]*PassKeyChain
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
		plugins: make([]*kubeletconfigv1.CredentialProvider, 0, len(config.Providers)),
		binDir:  binDir,
		cache:   make(map[string]*kubeletCredential),
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
// It first checks the cache using the same cacheKeyType-based lookup as the
// kubelet (image -> registry -> global). On a cache miss it executes all
// matching plugins, stores results keyed by cacheKeyType, and returns the most
// specific match for the requested ref.
func (p *KubeletProvider) GetCredentials(req *AuthRequest) (*PassKeyChain, error) {
	if req == nil || req.Ref == "" {
		return nil, errors.New("ref not found in request")
	}

	// Normalize the reference to handle short refs like "nginx:latest" -> "docker.io/library/nginx:latest"
	refSpec, _, err := parseReference(req.Ref)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse image reference")
	}

	image := refSpec.String()

	// Evict expired before adding new ones to bound the map size
	p.evictExpired()

	// Fast path: serve from cache when a valid-long-enough credential exists.
	// Lookup order follows the kubelet: image key, registry key, global key.
	cred := p.getCachedCredential(image, req.ValidUntil)
	if cred != nil {
		if kc := bestKeychainMatch(cred.keychains, image); kc != nil {
			log.L.WithField("ref", req.Ref).Debug("serving kubelet credentials from cache")
			return kc, nil
		}
	}

	// Slow path: execute matching plugins
	allKeychains := make(map[string]*PassKeyChain)
	for _, plugin := range p.plugins {
		if !isImageAllowed(plugin, image) {
			continue
		}

		// The spec mentions that:
		// "If one of the strings matches the requested image from the kubelet, the plugin will be invoked and given a chance to provide credentials"
		// So each matching plugin should have a chance to fetch credentials
		// TODO: parallelize?
		resp, err := p.execPlugin(context.Background(), plugin, image)
		if err != nil {
			log.L.WithError(err).WithFields(map[string]any{"plugin": plugin.Name, "ref": req.Ref}).Warn("failed to execute credential provider plugin")
			continue
		}

		if resp.Auth == nil {
			// Spec: A plugin should set this field (Auth) to null if no valid credentials can be returned for the requested image.
			continue
		}

		cacheKey := computeCacheKey(resp.CacheKeyType, image)
		if cacheKey == "" {
			log.L.WithFields(map[string]any{
				"plugin":       plugin.Name,
				"cacheKeyType": resp.CacheKeyType,
			}).Warn("credential provider plugin returned invalid cacheKeyType")
			continue
		}

		keychains := make(map[string]*PassKeyChain, len(resp.Auth))
		for registryGlob, authConfig := range resp.Auth {
			// First plugin wins for overlapping auth keys, matching the kubelet spec:
			// "If providers return overlapping auth keys, the value from the provider
			// earlier in this list is used."
			if _, exists := allKeychains[registryGlob]; exists {
				continue
			}
			kc := &PassKeyChain{
				Username: authConfig.Username,
				Password: authConfig.Password,
			}
			keychains[registryGlob] = kc
			allKeychains[registryGlob] = kc
		}

		var expiresAt time.Time
		if d := resolveCacheDuration(resp, plugin); d > 0 {
			expiresAt = time.Now().Add(d)
		}

		// Only cache when the plugin provides a TTL.
		// A zero duration means no caching (plugin will be re-executed on the next request).
		if !expiresAt.IsZero() {
			log.L.WithFields(map[string]any{
				"cacheKeyType": resp.CacheKeyType,
				"cacheKey":     cacheKey,
				"duration":     time.Until(expiresAt).Round(time.Second),
				"registries":   slices.Collect(maps.Keys(keychains)),
			}).Debug("caching kubelet credential provider response")

			p.mu.Lock()
			p.cache[cacheKey] = &kubeletCredential{
				keychains: keychains,
				expiresAt: expiresAt,
			}
			p.mu.Unlock()
		}
	}

	if len(allKeychains) == 0 {
		return nil, errors.New("no credentials found")
	}

	kc := bestKeychainMatch(allKeychains, image)
	if kc == nil {
		return nil, errors.New("no matching registries found")
	}
	return kc, nil
}

// evictExpired removes all expired entries from p.cache.
func (p *KubeletProvider) evictExpired() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for key, cred := range p.cache {
		if cred.expiresAt.IsZero() || now.After(cred.expiresAt) {
			delete(p.cache, key)
		}
	}
}

// getCachedCredential looks up a cached credential for the given image using the
// same lookup order as the kubelet: image key, registry key, then global key.
// Returns nil when no valid, non-expired match exists that satisfies validUntil.
func (p *KubeletProvider) getCachedCredential(image string, validUntil time.Time) *kubeletCredential {
	p.mu.RLock()
	defer p.mu.RUnlock()

	now := time.Now()
	for _, key := range []string{image, parseRegistry(image), globalCacheKey} {
		cred, ok := p.cache[key]
		if !ok {
			continue
		}
		if cred.expiresAt.IsZero() || now.After(cred.expiresAt) {
			continue
		}
		if !validUntil.IsZero() && !cred.expiresAt.After(validUntil) {
			continue
		}
		return cred
	}
	return nil
}

// bestKeychainMatch returns the most specific PassKeyChain whose registry glob
// matches image, using reverse-alphabetical order (longer/more specific paths first).
func bestKeychainMatch(keychains map[string]*PassKeyChain, image string) *PassKeyChain {
	var matching []string
	for registry := range keychains {
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
	// Sort in reverse alphabetical order: longer/more specific paths sort first.
	// For example, "gcr.io/etcd-development" matches before "gcr.io".
	sort.Sort(sort.Reverse(sort.StringSlice(matching)))
	return keychains[matching[0]]
}

// computeCacheKey determines the cache key for a plugin response based on the
// cacheKeyType field, following the same logic as the kubelet.
// Inspired from https://github.com/kubernetes/kubernetes/blob/v1.35.0/pkg/credentialprovider/plugin/plugin.go#L545
func computeCacheKey(cacheKeyType credentialproviderv1.PluginCacheKeyType, image string) string {
	switch cacheKeyType {
	case credentialproviderv1.ImagePluginCacheKeyType:
		return image
	case credentialproviderv1.RegistryPluginCacheKeyType:
		return parseRegistry(image)
	case credentialproviderv1.GlobalPluginCacheKeyType:
		return globalCacheKey
	default:
		return ""
	}
}

// parseRegistry extracts the registry (host and port) from an image string.
// Ported: https://github.com/kubernetes/kubernetes/blob/v1.35.0/pkg/credentialprovider/plugin/plugin.go#L592
func parseRegistry(image string) string {
	imageParts := strings.Split(image, "/")
	return imageParts[0]
}

// resolveCacheDuration picks the effective TTL from a plugin response:
// the per-response CacheDuration takes precedence if it is positive,
// otherwise the plugin's DefaultCacheDuration is used.
func resolveCacheDuration(resp *credentialproviderv1.CredentialProviderResponse, plugin *kubeletconfigv1.CredentialProvider) time.Duration {
	if resp.CacheDuration != nil && resp.CacheDuration.Duration >= 0 {
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
