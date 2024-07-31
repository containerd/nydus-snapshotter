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

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/pkg/errors"
)

type prefetchlist struct {
	FilePaths []string `json:"files"`
}

func GetPrefetchList(prefetchDir, imageRepo string) (string, error) {
	if config.IsPrefetchEnabled() {
		url := config.GetPrefetchEndpoint()
		getURL := fmt.Sprintf("%s?imageName=%s", url, imageRepo)

		resp, err := http.Get(getURL)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
			return "", fmt.Errorf("get from server returned a non-OK status code: %d, HTTP Status Error", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}

		if strings.Contains(string(body), "CacheItem not found") {
			log.L.Infof("Cache item not found for image: %s\n", imageRepo)
			return "", nil
		}

		prefetchfilePath, err := storePrefetchList(prefetchDir, body)
		if err != nil {
			return "", err
		}
		return prefetchfilePath, nil
	}
	return "", nil
}

func storePrefetchList(prefetchDir string, list []byte) (string, error) {
	if err := os.MkdirAll(prefetchDir, 0755); err != nil {
		return "", errors.Wrapf(err, "create prefetch dir %s", prefetchDir)
	}

	filePath := filepath.Join(prefetchDir, "list")
	jsonfilePath := filepath.Join(prefetchDir, "list.json")

	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
	if err != nil {
		fmt.Println("Error opening file:", err)
		return "", errors.Wrap(err, "error opening prefetch file")
	}
	defer file.Close()

	var prefetchSlice []string
	err = json.Unmarshal(list, &prefetchSlice)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse prefetch list")
	}

	for _, path := range prefetchSlice {
		content := path + "\n"
		_, err := file.WriteString(content)
		if err != nil {
			return "", errors.Wrap(err, "error writing to prefetch file")
		}
	}

	prefetchStruct := prefetchlist{FilePaths: prefetchSlice}
	jsonByte, err := json.Marshal(prefetchStruct)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal to JSON")
	}

	jsonfile, err := os.Create(jsonfilePath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to create file %s", jsonfilePath)
	}
	defer jsonfile.Close()

	_, err = jsonfile.Write(jsonByte)
	if err != nil {
		return "", errors.Wrap(err, "error writing JSON to file")
	}

	return filePath, nil
}
