/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package prefetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
)

type prefetchlist struct {
	FilePaths []string `json:"files"`
}

const (
	endpointPrefetch = "/api/v1/imagename"
	udsSocket        = "/run/optimizer/prefetch.sock"
)

var ErrUds = errors.New("failed to connect unix domain socket")

func GetPrefetchList(prefetchDir, imageRepo string) (string, error) {
	url := fmt.Sprintf("http://unix%s", endpointPrefetch)

	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(imageRepo))
	if err != nil {
		return "", err
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", udsSocket)
			},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		log.L.Infof("failed to connect unix domain socket. Skipping prefetch for image: %s\n", imageRepo)
		return "", ErrUds
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to send data, status code: %v", resp.StatusCode)
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

func storePrefetchList(prefetchDir string, list []byte) (string, error) {
	if err := os.MkdirAll(prefetchDir, 0755); err != nil {
		return "", errors.Wrapf(err, "create prefetch dir %s", prefetchDir)
	}

	filePath := filepath.Join(prefetchDir, "prefetchList")
	jsonfilePath := filepath.Join(prefetchDir, "prefetchList.json")

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
