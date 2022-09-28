/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package nydussdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/nydussdk/model"
	"github.com/containerd/nydus-snapshotter/pkg/utils/retry"
)

const (
	endpointDaemonInfo = "/api/v1/daemon"
	endpointMount      = "/api/v1/mount"
	endpointMetrics    = "/api/v1/metrics"
	endpointBlobs      = "/api/v2/blobs"

	defaultHTTPClientTimeout = 30 * time.Second
	jsonContentType          = "application/json"
)

type Interface interface {
	CheckStatus() (*model.DaemonInfo, error)
	SharedMount(sharedMountPoint, bootstrap, daemonConfig string) error
	FscacheBindBlob(daemonConfig string) error
	FscacheUnbindBlob(daemonConfig string) error
	Umount(sharedMountPoint string) error
	GetFsMetric(sharedDaemon bool, sid string) (*model.FsMetric, error)
}

type NydusdClient struct {
	httpClient *http.Client
}

type query = url.Values

func (c *NydusdClient) url(path string, query query) (url string) {
	url = fmt.Sprintf("http://unix%s", path)

	if len(query) != 0 {
		url += "?" + query.Encode()
	}

	return
}

func succeeded(resp *http.Response) bool {
	return resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK
}

func decode(resp *http.Response, v interface{}) error {
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return errors.Wrap(err, "decode response")
	}

	return nil
}

func parseErrorMessage(resp *http.Response) error {
	var errMessage model.ErrorMessage
	err := decode(resp, &errMessage)
	if err != nil {
		return err
	}

	return errors.Errorf("http response: %d, error code: %s, error message: %s",
		resp.StatusCode, errMessage.Code, errMessage.Message)
}

func buildTransport(sock string) (http.RoundTripper, error) {
	return &http.Transport{
		MaxIdleConns:          10,
		IdleConnTimeout:       10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := &net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 5 * time.Second,
			}
			return dialer.DialContext(ctx, "unix", sock)
		},
	}, nil
}

func WaitUntilSocketExisted(sock string) error {
	return retry.Do(func() (err error) {
		var st fs.FileInfo
		if st, err = os.Stat(sock); err != nil {
			return
		}

		if st.Mode()&os.ModeSocket == 0 {
			return errors.Errorf("file %s is not socket file", sock)
		}

		return nil
	},
		retry.Attempts(20), // totally wait for 2 seconds, should be enough
		retry.LastErrorOnly(true),
		retry.Delay(100*time.Millisecond))
}

func NewNydusClient(sock string) (Interface, error) {
	err := WaitUntilSocketExisted(sock)
	if err != nil {
		return nil, err
	}
	transport, err := buildTransport(sock)
	if err != nil {
		return nil, errors.Wrap(err, "build nydusd http client")
	}
	return &NydusdClient{
		httpClient: &http.Client{
			Timeout:   defaultHTTPClientTimeout,
			Transport: transport,
		},
	}, nil
}

func (c *NydusdClient) CheckStatus() (*model.DaemonInfo, error) {
	url := c.url(endpointDaemonInfo, query{})
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, errors.Wrapf(err, "get daemon info %s", url)
	}
	defer resp.Body.Close()

	if succeeded(resp) {
		var info model.DaemonInfo
		if err = decode(resp, &info); err != nil {
			return nil, err
		}
		return &info, nil
	}

	return nil, parseErrorMessage(resp)
}

func (c *NydusdClient) Umount(mp string) error {
	query := query{}
	query.Add("mountpoint", mp)
	url := c.url(endpointMount, query)
	return c.simpleRequest(http.MethodDelete, url)
}

func (c *NydusdClient) GetFsMetric(sharedDaemon bool, sid string) (*model.FsMetric, error) {
	query := query{}
	if sharedDaemon {
		query.Add("id", "/"+sid)
	}

	url := c.url(endpointMetrics, query)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "construct request url %s", url)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if !succeeded(resp) {
		return nil, parseErrorMessage(resp)
	}

	var m model.FsMetric
	err = decode(resp, &m)
	if err != nil {
		return nil, err
	}

	return &m, nil
}

func (c *NydusdClient) SharedMount(mp, bootstrap, daemonConfig string) error {
	config, err := os.ReadFile(daemonConfig)
	if err != nil {
		return errors.Wrapf(err, "read nydusd configurations %s", daemonConfig)
	}

	body, err := json.Marshal(model.NewMountRequest(bootstrap, string(config)))
	if err != nil {
		return errors.Wrap(err, "construct mount request")
	}

	query := query{}
	query.Add("mountpoint", mp)
	url := c.url(endpointMount, query)

	resp, err := c.httpClient.Post(url, jsonContentType, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if succeeded(resp) {
		return nil
	}

	return parseErrorMessage(resp)
}

func (c *NydusdClient) FscacheBindBlob(daemonConfig string) error {
	// FIXME: it brings extra IO latency!
	body, err := os.ReadFile(daemonConfig)
	if err != nil {
		return errors.Wrapf(err, "read daemon configuration %s", daemonConfig)
	}

	url := c.url(endpointBlobs, query{})
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewBuffer(body))
	if err != nil {
		return errors.Wrapf(err, "construct request, url %s", url)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if succeeded(resp) {
		return nil
	}

	return parseErrorMessage(resp)
}

func (c *NydusdClient) FscacheUnbindBlob(daemonConfig string) error {
	f, err := os.ReadFile(daemonConfig)
	if err != nil {
		return errors.Wrapf(err, "read daemon configuration %s", daemonConfig)
	}

	var cfg config.DaemonConfig
	if err := json.Unmarshal(f, &cfg); err != nil {
		return errors.Wrap(err, "unmarshal daemon configuration")
	}

	query := query{}
	query.Add("domain_id", cfg.DomainID)
	url := c.url(endpointBlobs, query)

	return c.simpleRequest(http.MethodDelete, url)
}
