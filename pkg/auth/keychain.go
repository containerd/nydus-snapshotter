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

// GetRegistryKeyChain get image pull keychain from (ordered):
// 1. username and secrets labels
// 2. cri request
// 3. docker config
// 4. kubelet credential helpers
// 5. k8s docker config secret
func GetRegistryKeyChain(ref string, labels map[string]string) *PassKeyChain {
	authRequest := &AuthRequest{
		Ref:    ref,
		Labels: labels,
	}

	errs := []error{}
	kc, err := NewLabelsProvider().GetCredentials(authRequest)
	if kc != nil {
		return kc
	}
	if err != nil {
		errs = append(errs, errors.Wrap(err, "get credentials from labels"))
	}

	kc, err = NewCRIProvider().GetCredentials(authRequest)
	if kc != nil {
		return kc
	}
	if err != nil {
		errs = append(errs, errors.Wrap(err, "get credentials from CRI"))
	}

	kc, err = NewDockerProvider().GetCredentials(authRequest)
	if kc != nil {
		return kc
	}
	if err != nil {
		errs = append(errs, errors.Wrap(err, "get credentials from Docker config"))
	}

	if kubeletProvider != nil {
		kc, err = kubeletProvider.GetCredentials(authRequest)
		if kc != nil {
			return kc
		}
		if err != nil {
			errs = append(errs, errors.Wrap(err, "get credentials from Kubelet credential helpers"))
		}
	}

	kc, err = NewKubeSecretProvider().GetCredentials(authRequest)
	if kc != nil {
		return kc
	}
	if err != nil {
		errs = append(errs, errors.Wrap(err, "get credentials from Kubernetes secrets"))
	}

	// Only output the errors if we did not manage to get a keychain
	if len(errs) > 0 {
		log.L.WithError(stderrors.Join(errs...)).WithField("ref", ref).Warn("Could not get registry credentials.")
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
