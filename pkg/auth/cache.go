/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"sync"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/utils/registry"
	"github.com/golang/groupcache/lru"
	"github.com/pkg/errors"
)

type Cache struct {
	lock  sync.RWMutex
	cache *lru.Cache
}

func NewCache() *Cache {
	return &Cache{
		cache: lru.New(30),
	}
}

func (c *Cache) UpdateAuth(imageHost, auth string) error {
	log.L.Debugf("update auth for %s", imageHost)
	key, err := AddKeyring(imageHost, auth)
	if err != nil {
		return err
	}
	data, err := getData(key)
	if err != nil {
		return err
	}

	c.lock.Lock()
	defer c.lock.Unlock()
	c.cache.Add(imageHost, data)

	return nil
}

func (c *Cache) GetAuth(imageHost string) (string, error) {
	log.L.Debugf("get auth for %s", imageHost)
	if auth, ok := c.cache.Get(imageHost); ok {
		return auth.(string), nil
	}

	data, err := SearchKeyring(imageHost)
	if err != nil {
		return "", errors.Wrap(err, "search key error")

	}

	c.lock.Lock()
	defer c.lock.Unlock()
	c.cache.Add(imageHost, data)

	return data, err
}

func (c *Cache) GetKeyChain(imageID string) (*PassKeyChain, error) {
	image, err := registry.ParseImage(imageID)
	if err != nil {
		return nil, errors.Wrapf(err, "parse image %s", imageID)
	}

	cachedAuth, err := c.GetAuth(image.Host)
	if err != nil {
		return nil, err
	}

	keyChain, err := FromBase64(cachedAuth)
	if err != nil {
		return nil, err
	}

	return &keyChain, nil
}
