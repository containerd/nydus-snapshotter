/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package prefetch

import (
	"encoding/json"
	"sync"

	"github.com/containerd/containerd/log"
)

type prefetchInfo struct {
	prefetchMap   map[string]string
	prefetchMutex sync.Mutex
}

var Pm prefetchInfo

func (p *prefetchInfo) SetPrefetchFiles(body []byte) error {
	p.prefetchMutex.Lock()
	defer p.prefetchMutex.Unlock()

	var prefetchMsg []map[string]string
	if err := json.Unmarshal(body, &prefetchMsg); err != nil {
		return err
	}

	if p.prefetchMap == nil {
		p.prefetchMap = make(map[string]string)
	}
	for _, item := range prefetchMsg {
		image := item["image"]
		prefetchfiles := item["prefetch"]
		p.prefetchMap[image] = prefetchfiles
	}

	log.L.Infof("received prefetch list from nri plugin: %v ", p.prefetchMap)
	return nil
}

func (p *prefetchInfo) GetPrefetchInfo(image string) string {
	p.prefetchMutex.Lock()
	defer p.prefetchMutex.Unlock()

	if prefetchfiles, ok := p.prefetchMap[image]; ok {
		return prefetchfiles
	}
	return ""
}

func (p *prefetchInfo) DeleteFromPrefetchMap(image string) {
	p.prefetchMutex.Lock()
	defer p.prefetchMutex.Unlock()

	delete(p.prefetchMap, image)
}
