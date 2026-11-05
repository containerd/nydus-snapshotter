/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package mount

import (
	"syscall"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsDisconnected(t *testing.T) {
	assert.True(t, isDisconnected(syscall.ENOTCONN))
	assert.True(t, isDisconnected(syscall.ESTALE))
	// Must see through pkg/errors wrapping (IsMountpoint wraps stat errors).
	assert.True(t, isDisconnected(errors.Wrapf(syscall.ENOTCONN, "stat target of %s", "/foo")))
	assert.False(t, isDisconnected(syscall.EBUSY))
	assert.False(t, isDisconnected(errors.New("some other error")))
	assert.False(t, isDisconnected(nil))
}

func TestUnmountWithFallback(t *testing.T) {
	origUnmount := syscallUnmount
	t.Cleanup(func() { syscallUnmount = origUnmount })

	type call struct {
		flags int
	}

	tests := []struct {
		name         string
		errByAttempt []error // error returned for the Nth syscallUnmount call
		wantUnclean  bool    // freed the path, but not by a plain unmount
		wantErr      bool
		wantCalls    []call
	}{
		{
			name:         "plain umount succeeds",
			errByAttempt: []error{nil},
			wantCalls:    []call{{0}},
		},
		{
			name:         "EINVAL treated as already unmounted",
			errByAttempt: []error{syscall.EINVAL},
			wantCalls:    []call{{0}},
		},
		{
			name:         "EBUSY falls back to force and reports it as unclean",
			errByAttempt: []error{syscall.EBUSY, nil},
			wantUnclean:  true,
			wantCalls:    []call{{0}, {umountForce}},
		},
		{
			name:         "force fails then lazy detach reports it as unclean",
			errByAttempt: []error{syscall.EBUSY, syscall.EBUSY, nil},
			wantUnclean:  true,
			wantCalls:    []call{{0}, {umountForce}, {umountDetach}},
		},
		{
			name:         "all attempts fail returns error",
			errByAttempt: []error{syscall.EBUSY, syscall.EBUSY, syscall.EBUSY},
			wantErr:      true,
			wantCalls:    []call{{0}, {umountForce}, {umountDetach}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotCalls []call
			idx := 0
			syscallUnmount = func(_ string, flags int) error {
				gotCalls = append(gotCalls, call{flags})
				err := tc.errByAttempt[idx]
				idx++
				return err
			}

			err := unmountWithFallback("/mnt/target")
			switch {
			case tc.wantErr:
				require.Error(t, err)
				assert.NotErrorIs(t, err, ErrUnmountedUnclean)
			case tc.wantUnclean:
				// An unclean teardown must not be reported as success.
				require.ErrorIs(t, err, ErrUnmountedUnclean)
			default:
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantCalls, gotCalls)
		})
	}
}

func TestUmount(t *testing.T) {
	origIsMountpoint := isMountpoint
	origUnmount := syscallUnmount
	t.Cleanup(func() {
		isMountpoint = origIsMountpoint
		syscallUnmount = origUnmount
	})

	t.Run("disconnected mountpoint still gets unmounted", func(t *testing.T) {
		isMountpoint = func(string) (bool, error) { return false, syscall.ENOTCONN }
		unmounted := false
		syscallUnmount = func(string, int) error { unmounted = true; return nil }

		require.NoError(t, (&Mounter{}).Umount("/mnt/target"))
		assert.True(t, unmounted, "a disconnected mountpoint must still be unmounted")
	})

	t.Run("not mounted returns nil without unmounting", func(t *testing.T) {
		isMountpoint = func(string) (bool, error) { return false, nil }
		called := false
		syscallUnmount = func(string, int) error { called = true; return nil }

		require.NoError(t, (&Mounter{}).Umount("/mnt/target"))
		assert.False(t, called, "must not attempt to unmount a non-mountpoint")
	})

	t.Run("other IsMountpoint error is propagated", func(t *testing.T) {
		wantErr := errors.New("boom")
		isMountpoint = func(string) (bool, error) { return false, wantErr }
		syscallUnmount = func(string, int) error { return nil }

		require.ErrorIs(t, (&Mounter{}).Umount("/mnt/target"), wantErr)
	})

	t.Run("unclean teardown is reported to the caller", func(t *testing.T) {
		isMountpoint = func(string) (bool, error) { return true, nil }
		syscallUnmount = func(_ string, flags int) error {
			if flags == 0 {
				return syscall.EBUSY
			}
			return nil
		}

		require.ErrorIs(t, (&Mounter{}).Umount("/mnt/target"), ErrUnmountedUnclean)
	})
}

func TestWaitUntilUnmountedReportsDisconnectedMountpoint(t *testing.T) {
	origIsMountpoint := isMountpoint
	t.Cleanup(func() { isMountpoint = origIsMountpoint })

	calls := 0
	isMountpoint = func(string) (bool, error) {
		calls++
		return false, syscall.ENOTCONN
	}

	// A disconnected mountpoint is still mounted, so this is not a successful
	// unmount; and since it never reconnects, the wait must not be retried.
	err := WaitUntilUnmounted("/mnt/target")
	require.ErrorIs(t, err, ErrMountpointDisconnected)
	require.ErrorIs(t, err, syscall.ENOTCONN)
	assert.Equal(t, 1, calls)
}
