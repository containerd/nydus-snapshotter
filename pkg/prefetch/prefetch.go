/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package prefetch

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
)

type prefetchInfo struct {
	prefetchMap   map[string]string
	prefetchMutex sync.Mutex
}

type prefetchlist struct {
	Files []string `json:"files"`
}

var Pm prefetchInfo

func (p *prefetchInfo) GetPrefetchMap(body []byte) error {
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
		url := item["prefetch"]
		p.prefetchMap[image] = url
	}

	log.L.Infof("received prefetch list from nri plugin: %v ", p.prefetchMap)
	return nil
}

func (p *prefetchInfo) GetPrefetchListPath(prefetchDir, imageID string) (string, error) {
	p.prefetchMutex.Lock()
	defer p.prefetchMutex.Unlock()

	var targetURL string
	var found bool
	for image, url := range p.prefetchMap {
		if image == imageID {
			targetURL = url
			found = true
			break
		}
	}
	if !found {
		log.L.Infof("the imageID %s not exist in prefetchMap", imageID)
		return "", nil
	}

	resp, err := http.Get(targetURL)
	if err != nil {
		return "", errors.Wrapf(err, "failed to GET %s", targetURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("failed to GET %s, response status %v", targetURL, resp.Status)
		return "", errors.New(errMsg)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Wrap(err, "failed to read response body")
	}

	if err = os.MkdirAll(prefetchDir, 0755); err != nil {
		return "", errors.Wrapf(err, "create prefetch dir %s", prefetchDir)
	}

	filePath := filepath.Join(prefetchDir, "prefetchList")
	jsonPath := filepath.Join(prefetchDir, "prefetchList.json")

	file, err := os.Create(filePath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to create file %s", filePath)
	}
	defer file.Close()

	if err = os.WriteFile(filePath, body, 0755); err != nil {
		return "", errors.Wrapf(err, "failed to write file %s", filePath)
	}

	bodyLines := strings.Split(string(body), "\n")
	prefetchFiles := prefetchlist{
		Files: bodyLines,
	}

	jsonfile, err := os.Create(jsonPath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to create file %s", jsonPath)
	}
	defer jsonfile.Close()

	data, err := json.Marshal(prefetchFiles)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal JSON data")
	}
	if err = os.WriteFile(jsonPath, data, 0755); err != nil {
		return "", errors.Wrapf(err, "failed to write file %s", jsonPath)
	}

	delete(p.prefetchMap, imageID)

	return filePath, nil
}
