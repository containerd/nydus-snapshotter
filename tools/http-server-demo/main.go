/*
* Copyright (c) 2023. Nydus Developers. All rights reserved.
*
* SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"
)

const defaultPrefetchListPath = "/var/prefetch/prefetchList"

// handlePostPrefetchList handles the POST request to save the prefetch list to a file.
func handlePostPrefetchList(c echo.Context) error {
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		log.Errorf("Failed to read request body: %v", err)
		return c.String(http.StatusBadRequest, "Bad Request")
	}

	prefetchList := string(body)
	log.Infof("received prefetch list from client: %v", prefetchList)

	err = savePrefetchListToFile(prefetchList)
	if err != nil {
		log.Errorf("Failed to save prefetch list to file: %v", err)
		return c.String(http.StatusInternalServerError, "Internal Server Error")
	}

	return c.String(http.StatusOK, "Successfully processed POST request")
}

// savePrefetchListToFile saves the provided prefetch list to a file specified by defaultPrefetchListPath.
func savePrefetchListToFile(prefetchList string) error {
	dir := filepath.Dir(defaultPrefetchListPath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	err := os.WriteFile(defaultPrefetchListPath, []byte(prefetchList), 0644)
	if err != nil {
		return err
	}

	return nil
}

// getPrefetchList retrieves the prefetch list from the file specified by defaultPrefetchListPath and returns it.
func getPrefetchList(c echo.Context) error {
	prefetchList, err := os.ReadFile(defaultPrefetchListPath)
	if err != nil {
		log.Errorf("Failed to read prefetch list: %v", err)
		return c.String(http.StatusInternalServerError, "Internal Server Error")
	}

	return c.String(http.StatusOK, string(prefetchList))
}

func main() {
	e := echo.New()

	e.Logger = log.New("echo")
	e.Logger.SetLevel(log.INFO)

	e.POST("/api/v1/post/prefetch", handlePostPrefetchList)
	e.GET("/api/v1/get/prefetch", getPrefetchList)

	if err := e.Start(":1323"); err != nil {
		e.Logger.Fatal(err)
	}
}
