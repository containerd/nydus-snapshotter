/*
* Copyright (c) 2023. Nydus Developers. All rights reserved.
*
* SPDX-License-Identifier: Apache-2.0
 */
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/pkg/errors"
)

type LRUItem struct {
	CacheItem *CacheItem
	Prev      *LRUItem
	Next      *LRUItem
}

type CacheItem struct {
	ImageName     string
	ContainerName string
	PrefetchFiles []string
}

type Cache struct {
	Items   map[string]*LRUItem
	Head    *LRUItem
	Tail    *LRUItem
	MaxSize int
	mutex   sync.Mutex
}

func (cache *Cache) key(imageName, containerName string) string {
	return fmt.Sprintf("%s,%s", imageName, containerName)
}

func (cache *Cache) Get(imageName string) ([]string, error) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	var latestItem *LRUItem

	for _, lruItem := range cache.Items {
		if lruItem.CacheItem.ImageName == imageName {
			latestItem = lruItem
			break
		}
	}

	if latestItem == nil {
		return nil, errors.New("item not found in cache")
	}

	cache.removeNode(latestItem)
	cache.addtoHead(cache.Head, latestItem)

	return latestItem.CacheItem.PrefetchFiles, nil
}

func (cache *Cache) Set(item CacheItem) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	key := cache.key(item.ImageName, item.ContainerName)
	if lruItem, exists := cache.Items[key]; exists {
		cache.removeNode(lruItem)
		cache.addtoHead(cache.Head, lruItem)

		lruItem.CacheItem = &item
	} else {
		newLRUItem := &LRUItem{
			CacheItem: &item,
		}

		cache.addtoHead(cache.Head, newLRUItem)

		cache.Items[key] = newLRUItem

		if len(cache.Items) > cache.MaxSize {
			tail := cache.Tail.Prev
			cache.removeNode(tail)
			delete(cache.Items, cache.key(tail.CacheItem.ImageName, tail.CacheItem.ContainerName))
		}
	}
}

func (cache *Cache) removeNode(lruItem *LRUItem) {
	lruItem.Prev.Next = lruItem.Next
	lruItem.Next.Prev = lruItem.Prev
}

func (cache *Cache) addtoHead(head, lruItem *LRUItem) {
	next := head.Next
	head.Next = lruItem
	lruItem.Prev = head
	lruItem.Next = next
	next.Prev = lruItem
}

var serverCache Cache

func uploadHandler(c echo.Context) error {
	var item CacheItem
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.String(http.StatusBadRequest, "Failed to read request body")
	}
	err = json.Unmarshal(body, &item)
	if err != nil {
		return c.String(http.StatusBadRequest, "Invalid request payload")
	}

	serverCache.Set(item)
	return c.String(http.StatusOK, fmt.Sprintf("Uploaded CacheItem for %s, %s successfully", item.ImageName, item.ContainerName))
}

func downloadHandler(c echo.Context) error {
	imageName := c.QueryParam("imageName")

	item, err := serverCache.Get(imageName)
	if err != nil {
		return c.String(http.StatusNotFound, "CacheItem not found")
	}

	return c.JSON(http.StatusOK, item)

}

func main() {
	head, tail := &LRUItem{}, &LRUItem{}
	head.Next = tail
	tail.Prev = head
	serverCache = Cache{
		Items:   make(map[string]*LRUItem),
		Head:    head,
		Tail:    tail,
		MaxSize: 1000,
	}

	e := echo.New()
	e.POST("/api/v1/prefetch/upload", uploadHandler)
	e.GET("/api/v1/prefetch", downloadHandler)

	e.Logger.Fatal(e.Start(":1323"))
}
