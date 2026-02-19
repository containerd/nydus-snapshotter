/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"encoding/base64"
	stderrors "errors"
	"fmt"
	"strings"

	"github.com/containerd/log"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/pkg/errors"
)

const (
	sep = ":"
)

var (
	emptyPassKeyChain = PassKeyChain{}
)

// PassKeyChain is user/password based key chain
type PassKeyChain struct {
	Username string
	Password string
}

func FromBase64(str string) (PassKeyChain, error) {
	decoded, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return emptyPassKeyChain, err
	}
	pair := strings.Split(string(decoded), sep)
	if len(pair) != 2 {
		return emptyPassKeyChain, errors.New("invalid registry auth token")
	}
	return PassKeyChain{
		Username: pair[0],
		Password: pair[1],
	}, nil
}

func (kc PassKeyChain) ToBase64() string {
	if kc.Username == "" && kc.Password == "" {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", kc.Username, kc.Password)))
}

// TokenBase check if PassKeyChain is token based, when username is empty and password is not empty
// then password is registry token
func (kc PassKeyChain) TokenBase() bool {
	return kc.Username == "" && kc.Password != ""
}

// buildProviders returns the ordered list of auth providers.
// Priority: labels > CRI > docker > kubelet > kubesecret.
// It is a variable so tests can substitute a different builder.
var buildProviders = func() []AuthProvider {
	providers := []AuthProvider{
		NewLabelsProvider(),
		NewCRIProvider(),
		NewDockerProvider(),
	}
	if kubeletProvider != nil {
		providers = append(providers, kubeletProvider)
	}
	providers = append(providers, NewKubeSecretProvider())
	return providers
}

// GetRegistryKeyChain retrieves image pull credentials from the first provider
// that returns a result, checked in priority order:
// 1. credential renewal store (if enabled)
// 2. username and secrets labels
// 3. cri request
// 4. docker config
// 5. kubelet credential helpers
// 6. k8s docker config secret
//
// When a renewable provider returns credentials and the renewal store is
// enabled, the credentials are cached for periodic renewal.
func GetRegistryKeyChain(ref string, labels map[string]string) *PassKeyChain {
	return getRegistryKeyChainFromProviders(ref, labels, buildProviders())
}

// getRegistryKeyChainFromProviders is the testable core of GetRegistryKeyChain.
func getRegistryKeyChainFromProviders(ref string, labels map[string]string, providers []AuthProvider) *PassKeyChain {
	logger := log.L.WithField("ref", ref)
	// Serve from the renewal store if available and not expired.
	if renewalStore != nil {
		if kc := renewalStore.Get(ref); kc != nil {
			logger.Debug("serving credentials from renewal store")
			return kc
		}
	}

	authRequest := &AuthRequest{Ref: ref, Labels: labels}

	var errs []error
	for _, provider := range providers {
		logger.Info(fmt.Sprintf("Trying to get credentials from %s", provider))
		kc, err := provider.GetCredentials(authRequest)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "get credentials from %T", provider))
		}
		if kc != nil {
			// Cache in the renewal store when the provider supports renewal.
			if renewalStore != nil {
				if rp, ok := provider.(RenewableProvider); ok && rp.CanRenew() {
					renewalStore.Add(ref, provider, kc)
				}
			}
			return kc
		}
	}

	if len(errs) > 0 {
		logger.WithError(stderrors.Join(errs...)).Warn("Could not get registry credentials.")
	}
	return nil
}

func GetKeyChainByRef(ref string, labels map[string]string) (*PassKeyChain, error) {
	keychain := GetRegistryKeyChain(ref, labels)

	return keychain, nil
}

func (kc PassKeyChain) Resolve(_ authn.Resource) (authn.Authenticator, error) {
	return authn.FromConfig(kc.toAuthConfig()), nil
}

// toAuthConfig convert PassKeyChain to authn.AuthConfig when kc is token based,
// RegistryToken is preferred to
func (kc PassKeyChain) toAuthConfig() authn.AuthConfig {
	if kc.TokenBase() {
		return authn.AuthConfig{
			RegistryToken: kc.Password,
		}
	}
	return authn.AuthConfig{
		Username: kc.Username,
		Password: kc.Password,
	}
}
