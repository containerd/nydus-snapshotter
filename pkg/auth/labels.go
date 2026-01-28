/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/pkg/errors"
)

// LabelsProvider retrieves credentials from snapshot labels.
type LabelsProvider struct{}

// NewLabelsProvider creates a new labels-based auth provider.
func NewLabelsProvider() *LabelsProvider {
	return &LabelsProvider{}
}

// GetCredentials retrieves credentials from snapshot labels.
// Returns nil if labels don't contain valid credentials.
func (p *LabelsProvider) GetCredentials(req *AuthRequest) (*PassKeyChain, error) {
	if req.Labels == nil {
		return nil, errors.New("labels not found")
	}

	u, found := req.Labels[label.NydusImagePullUsername]
	if !found || u == "" {
		return nil, errors.New("username label not found")
	}

	pass, found := req.Labels[label.NydusImagePullSecret]
	if !found || pass == "" {
		return nil, errors.New("password label not found")
	}

	return &PassKeyChain{
		Username: u,
		Password: pass,
	}, nil
}
