/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package errdefs

import (
	stderrors "errors"
	"fmt"
	"net"
	"syscall"

	"github.com/containerd/errdefs"
	"github.com/pkg/errors"
)

var (
	ErrAlreadyExists   = errdefs.ErrAlreadyExists
	ErrNotFound        = errdefs.ErrNotFound
	ErrInvalidArgument = errors.New("invalid argument")
	ErrUnavailable     = errors.New("unavailable")
	ErrNotImplemented  = errors.New("not implemented") // represents not supported and unimplemented
	ErrDeviceBusy      = errors.New("device busy")     // represents not supported and unimplemented
	// ErrFscacheCullPending means the filesystem was unmounted and unbound, but
	// cachefiles still needs to finish reclaiming the bootstrap cookie.
	ErrFscacheCullPending = errors.New("fscache cull pending")
)

type FscacheCullPendingError struct {
	BlobID string
	Reason string
}

func (e *FscacheCullPendingError) Error() string {
	return fmt.Sprintf("fscache blob %s cull is pending: %s", e.BlobID, e.Reason)
}

func (e *FscacheCullPendingError) Unwrap() error {
	return ErrFscacheCullPending
}

// IsAlreadyExists returns true if the error is due to already exists
func IsAlreadyExists(err error) bool {
	return errors.Is(err, ErrAlreadyExists)
}

// IsNotFound returns true if the error is due to a missing object
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
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
