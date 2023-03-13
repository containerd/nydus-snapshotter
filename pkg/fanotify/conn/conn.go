/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package conn

import (
	"bufio"
	"encoding/json"
	"io"
)

type Client struct {
	Reader *bufio.Reader
}

type EventInfo struct {
	Path      string `json:"path"`
	Size      uint32 `json:"size"`
	Timestamp int64  `json:"timestamp"`
}

func (c *Client) GetEventInfo() ([]EventInfo, error) {
	// Before reaching '\n', the reader has successfully read
	// the event information from optimizer server.
	data, err := c.Reader.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return nil, err
	}

	eventInfo := []EventInfo{}
	if err := json.Unmarshal(data, &eventInfo); err != nil {
		return nil, err
	}

	return eventInfo, nil
}
