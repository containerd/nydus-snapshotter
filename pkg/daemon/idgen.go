/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemon

import (
	"github.com/rs/xid"
)

func newID() string {
	return xid.New().String()
}
