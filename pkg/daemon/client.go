/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/pkg/errors"

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/tool"
	"github.com/containerd/nydus-snapshotter/pkg/utils/retry"
)

const (
	// Get information about nydus daemon
	endpointDaemonInfo = "/api/v1/daemon"
	// Mount or umount filesystems.
	endpointMount = "/api/v1/mount"
	// Fetch generic filesystem metrics.
	endpointMetrics = "/api/v1/metrics"
	// Fetch metrics relevant to caches usage.
	endpointCacheMetrics = "/api/v1/metrics/blobcache"
	// Fetch metrics about inflighting operations.
	endpointInflightMetrics = "/api/v1/metrics/inflight"
	// Request nydus daemon to retrieve its runtime states from the supervisor, recovering states for failover.
	endpointTakeOver = "/api/v1/daemon/fuse/takeover"
	// Request nydus daemon to send its runtime states to the supervisor, preparing for failover.
	endpointSendFd = "/api/v1/daemon/fuse/sendfd"
	// Request nydus daemon to start filesystem service.
	endpointStart = "/api/v1/daemon/start"
	// Request nydus daemon to exit
	endpointExit = "/api/v1/daemon/exit"

	// --- V2 API begins
	// Add/remove blobs managed by the blob cache manager.
	endpointBlobs = "/api/v2/blobs"

	defaultHTTPClientTimeout = 30 * time.Second

	jsonContentType = "application/json"
)

// Nydusd HTTP client to query nydusd runtime status, operate file system instances.
// Control nydusd workflow like failover and upgrade.
type NydusdClient interface {
	GetDaemonInfo() (*types.DaemonInfo, error)

	Mount(mountpoint, bootstrap, daemonConfig string) error
	Umount(mountpoint string) error

	BindBlob(daemonConfig string) error
	UnbindBlob(domainID, blobID string) error

	GetFsMetrics(sid string) (*types.FsMetrics, error)
	GetInflightMetrics() (*types.InflightMetrics, error)
	GetCacheMetrics(sid string) (*types.CacheMetrics, error)

	TakeOver() error
	SendFd() error
	Start() error
	Exit() error
}

// Nydusd API server http client used to command nydusd's action and
// query nydusd working status.
type nydusdClient struct {
	httpClient *http.Client
}

type query = url.Values

func (c *nydusdClient) url(path string, query query) (url string) {
	url = fmt.Sprintf("http://unix%s", path)

	if len(query) != 0 {
		url += "?" + query.Encode()
	}

	return
}

// A simple http client request wrapper with capability to take
// request body and handle or process http response if result is expected.
func (c *nydusdClient) request(method string, url string,
	body io.Reader, respHandler func(resp *http.Response) error) error {

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return errors.Wrapf(err, "construct request %s", url)
	}

	if body != nil {
		req.Header.Add("Content-Type", jsonContentType)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if succeeded(resp) {
		if respHandler != nil {
			if err = respHandler(resp); err != nil {
				return errors.Wrapf(err, "handle response")
			}
		}
		return nil
	}

	return parseErrorMessage(resp)
}

func succeeded(resp *http.Response) bool {
	return resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK
}

func decode(resp *http.Response, v any) error {
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return errors.Wrap(err, "decode response")
	}

	return nil
}

// Parse http response to get the specific error message formatted by nydusd API server.
// So it will be clear what's wrong in nydusd during processing http requests.
func parseErrorMessage(resp *http.Response) error {
	var errMessage types.ErrorMessage
	err := decode(resp, &errMessage)
	if err != nil {
		return err
	}

	return errors.Errorf("http response: %d, error code: %s, error message: %s",
		resp.StatusCode, errMessage.Code, errMessage.Message)
}

func buildTransport(sock string) http.RoundTripper {
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
	}
}

func WaitUntilSocketExisted(sock string, pid int) error {
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
		retry.Attempts(100), // totally wait for 10 seconds, should be enough
		retry.LastErrorOnly(true),
		retry.Delay(100*time.Millisecond),
		retry.OnlyRetryIf(func(error) bool {
			zombie, err := tool.IsZombieProcess(pid)
			if err != nil {
				return false
			}
			// Stop retry if nydus daemon process is already in Zombie state.
			if zombie {
				log.L.Errorf("Process %d has been a zombie", pid)
				return true
			}
			return false
		}),
	)
}

func NewNydusClient(sock string) (NydusdClient, error) {
	transport := buildTransport(sock)
	return &nydusdClient{
		httpClient: &http.Client{
			Timeout:   defaultHTTPClientTimeout,
			Transport: transport,
		},
	}, nil
}

func (c *nydusdClient) GetDaemonInfo() (*types.DaemonInfo, error) {
	url := c.url(endpointDaemonInfo, query{})

	var info types.DaemonInfo
	err := c.request(http.MethodGet, url, nil, func(resp *http.Response) error {
		if err := decode(resp, &info); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return &info, nil
}

func (c *nydusdClient) Mount(mp, bootstrap, mountConfig string) error {
	cmd, err := json.Marshal(types.NewMountRequest(bootstrap, mountConfig))
	if err != nil {
		return errors.Wrap(err, "construct mount request")
	}

	query := query{}
	query.Add("mountpoint", mp)
	url := c.url(endpointMount, query)

	return c.request(http.MethodPost, url, bytes.NewBuffer(cmd), nil)
}

func (c *nydusdClient) Umount(mp string) error {
	query := query{}
	query.Add("mountpoint", mp)
	url := c.url(endpointMount, query)
	return c.request(http.MethodDelete, url, nil, nil)
}

func (c *nydusdClient) BindBlob(daemonConfig string) error {
	url := c.url(endpointBlobs, query{})
	return c.request(http.MethodPut, url, bytes.NewBuffer([]byte(daemonConfig)), nil)
}

// Delete /api/v2/blobs implements different functions according to different parameters
//  1. domainID , delete all blob entries in the domain.
//  2. domainID + blobID, delete the blob entry, if the blob is bootstrap
//     also delete blob entries belong to it.
//  3. blobID, try to find and cull blob cache files by blobID in all domains.
func (c *nydusdClient) UnbindBlob(domainID, blobID string) error {
	query := query{}
	if domainID != "" {
		query.Add("domain_id", domainID)
		if domainID != blobID {
			query.Add("blob_id", blobID)
		}
	} else {
		query.Add("blob_id", blobID)
	}

	url := c.url(endpointBlobs, query)

	return c.request(http.MethodDelete, url, nil, nil)
}

func (c *nydusdClient) GetFsMetrics(sid string) (*types.FsMetrics, error) {
	query := query{}
	if sid != "" {
		query.Add("id", "/"+sid)
	}

	url := c.url(endpointMetrics, query)
	var m types.FsMetrics
	if err := c.request(http.MethodGet, url, nil, func(resp *http.Response) error {
		return decode(resp, &m)
	}); err != nil {
		return nil, err
	}

	return &m, nil
}

func (c *nydusdClient) GetInflightMetrics() (*types.InflightMetrics, error) {
	url := c.url(endpointInflightMetrics, query{})
	var m types.InflightMetrics
	if err := c.request(http.MethodGet, url, nil, func(resp *http.Response) error {
		if resp.StatusCode != http.StatusNoContent {
			return decode(resp, &m.Values)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return &m, nil
}

func (c *nydusdClient) GetCacheMetrics(sid string) (*types.CacheMetrics, error) {
	query := query{}
	if sid != "" {
		query.Add("id", "/"+sid)
	}

	url := c.url(endpointCacheMetrics, query)
	var m types.CacheMetrics
	if err := c.request(http.MethodGet, url, nil, func(resp *http.Response) error {
		return decode(resp, &m)
	}); err != nil {
		return nil, err
	}

	return &m, nil
}

func (c *nydusdClient) TakeOver() error {
	url := c.url(endpointTakeOver, query{})
	return c.request(http.MethodPut, url, nil, nil)
}

func (c *nydusdClient) SendFd() error {
	url := c.url(endpointSendFd, query{})
	return c.request(http.MethodPut, url, nil, nil)
}

func (c *nydusdClient) Start() error {
	url := c.url(endpointStart, query{})
	return c.request(http.MethodPut, url, nil, nil)
}

func (c *nydusdClient) Exit() error {
	url := c.url(endpointExit, query{})
	return c.request(http.MethodPut, url, nil, nil)
}
