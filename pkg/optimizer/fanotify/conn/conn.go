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
	Path    string `json:"path"`
	Size    uint32 `json:"size"`
	Elapsed uint64 `json:"elapsed"`
}

func (c *Client) GetEventInfo() (*EventInfo, error) {
	eventInfo := EventInfo{}

	eventByte, err := c.Reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(eventByte, &eventInfo); err != nil {
		return nil, err
	}

	return &eventInfo, nil
}
