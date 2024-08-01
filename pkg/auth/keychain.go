/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/pkg/label"
	distribution "github.com/distribution/reference"
	"github.com/google/go-containerregistry/pkg/authn"
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

// FromLabels finds image pull username and secret from snapshot labels.
// Returned `nil` means no valid username and secret is passed, it should
// not override input nydusd configuration.
func FromLabels(labels map[string]string) *PassKeyChain {
	u, found := labels[label.NydusImagePullUsername]
	if !found || u == "" {
		return nil
	}

	p, found := labels[label.NydusImagePullSecret]
	if !found || p == "" {
		return nil
	}

	return &PassKeyChain{
		Username: u,
		Password: p,
	}
}

// GetRegistryKeyChain get image pull keychain from (ordered):
// 1. username and secrets labels
// 2. cri request
// 3. docker config
// 4. k8s docker config secret
func GetRegistryKeyChain(host, ref string, labels map[string]string) *PassKeyChain {
	kc := FromLabels(labels)
	if kc != nil {
		return kc
	}

	// TODO: Handle error
	kc, _ = FromCRI(host, ref)
	if kc != nil {
		return kc
	}

	kc = FromDockerConfig(host)
	if kc != nil {
		return kc
	}

	return FromKubeSecretDockerConfig(host)
}

func GetKeyChainByRef(ref string, labels map[string]string) (*PassKeyChain, error) {
	named, err := distribution.ParseDockerRef(ref)
	if err != nil {
		return nil, errors.Wrapf(err, "parse ref %s", ref)
	}

	host := distribution.Domain(named)
	keychain := GetRegistryKeyChain(host, ref, labels)

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
