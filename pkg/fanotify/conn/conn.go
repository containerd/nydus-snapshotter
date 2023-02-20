/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package conn

import (
	"bufio"
	"io"
)

type Client struct {
	Scanner *bufio.Scanner
}

func (c *Client) GetPath() (string, error) {
	if !c.Scanner.Scan() { // NOTE: no timeout
		return "", io.EOF
	}
	return c.Scanner.Text(), nil
}
