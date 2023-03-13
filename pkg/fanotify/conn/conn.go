/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package conn

import (
	"bufio"
	"encoding/json"
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
	eventInfo := []EventInfo{}

	err := json.NewDecoder(c.Reader).Decode(&eventInfo)
	if err != nil {
		return nil, err
	}

	return eventInfo, nil
}
