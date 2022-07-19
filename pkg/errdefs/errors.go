/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package errdefs

import (
	stderrors "errors"
	"net"
	"strings"
	"syscall"

	"github.com/pkg/errors"
)

const signalKilled = "signal: killed"

var (
	ErrAlreadyExists = errors.New("already exists")
	ErrMissingLabels = errors.New("missing labels")
)

// IsAlreadyExists returns true if the error is due to already exists
func IsAlreadyExists(err error) bool {
	return errors.Is(err, ErrAlreadyExists)
}

// IsMissingLabels returns true if the error is due to missing labels
func IsMissingLabels(err error) bool {
	return errors.Is(err, ErrMissingLabels)
}

// IsSignalKilled returns true if the error is signal killed
func IsSignalKilled(err error) bool {
	return strings.Contains(err.Error(), signalKilled)
}

// IsConnectionClosed returns true if error is due to connection closed
// this is used when snapshotter closed by sig term
func IsConnectionClosed(err error) bool {
	switch err := err.(type) {
	case *net.OpError:
		return err.Err.Error() == "use of closed network connection"
	default:
		return false
	}
}

func IsErofsMounted(err error) bool {
	return stderrors.Is(err, syscall.EBUSY)
}
