/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"strconv"
	"strings"
	"sync"

	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

const (
	defaultSessionName = "_ses.nydus"
)

var (
	once          sync.Once
	globalKeyRing KeyRing
)

type KeyRing struct {
	sessKeyID int
	keyLock   sync.RWMutex
	avaliable bool
}

func GetSessionID() (int, error) {
	var joinSessionErr error
	once.Do(
		func() {
			sessKeyID, err := unix.KeyctlJoinSessionKeyring(defaultSessionName)
			if err != nil {
				joinSessionErr = err
				return
			}

			log.L.Infof("joined the session keyring %s", defaultSessionName)

			if err := addSearchPermission(sessKeyID); err != nil {
				joinSessionErr = err
				return
			}

			log.L.Infof("added search permission for session keyring %s", defaultSessionName)

			globalKeyRing.sessKeyID = sessKeyID
			globalKeyRing.avaliable = true
		},
	)
	if joinSessionErr != nil {
		return 0, errors.Wrapf(joinSessionErr, "join session keyring %s.", defaultSessionName)
	}
	if !globalKeyRing.avaliable {
		return 0, unix.EINVAL
	}

	return globalKeyRing.sessKeyID, nil
}

func AddKeyring(id, value string) (int, error) {
	sessKeyID, err := GetSessionID()
	if err != nil {
		return 0, err
	}

	globalKeyRing.keyLock.Lock()
	defer globalKeyRing.keyLock.Unlock()

	keyID, err := unix.AddKey("user", id, []byte(value), sessKeyID)
	if err != nil {
		return 0, err
	}

	return keyID, nil
}

func checkPermission(ringID int, targetMask uint32) (uint32, bool, error) {
	var mask uint32 = 0xffffffff

	dest, err := unix.KeyctlString(unix.KEYCTL_DESCRIBE, ringID)
	if err != nil {
		return 0, false, err
	}

	/*
	 * An example output for KEYCTL_DESCRIBE: keyring;0;0;3f1b0000;_ses.nydus
	 * We only need the permission mask, so split it by ';'.
	 */
	res := strings.Split(dest, ";")
	if len(res) < 5 {
		return 0, false, errors.New("destination buffer for key description is too small")
	}

	perm64, err := strconv.ParseUint(res[3], 16, 32)
	if err != nil {
		return 0, false, err
	}

	permFull := uint32(perm64) & mask

	return permFull, (permFull & targetMask) != 0, nil
}

func addSearchPermission(ringID int) error {
	/*
	 * The permissions mask contains four sets of rights.
	 * 0x80000  ->  00000000 00001000 00000000 00000000
	 *               \    /   \    /   \    /    \   /
	 *              possessor  user     group    other
	 * For each set of rights, only the last of six bits is used.
	 * 00111111 ->  alswrv
	 *
	 * You can get this information via `keyctl describe [Session ID]`
	 *
	 * a: setattr
	 * l: link
	 * s: search
	 * w: write
	 * r: read
	 * v: view
	 *
	 * So, 0x80000 means add search right for user.
	 *
	 * Refer to https://man7.org/linux/man-pages/man7/keyrings.7.html
	 */
	var searchPermissionBits uint32 = 0x80000

	// Check if the search right for user already exists.
	permFull, hasPermission, err := checkPermission(ringID, searchPermissionBits)
	if err != nil {
		return errors.Wrap(err, "check permission")
	}
	if hasPermission {
		return nil
	}

	// Add search right for user.
	if err := unix.KeyctlSetperm(ringID, permFull|searchPermissionBits); err != nil {
		return errors.Wrap(err, "set permission")
	}

	permFull, hasPermission, err = checkPermission(ringID, searchPermissionBits)
	if err != nil {
		return errors.Wrap(err, "check permission after add search permission")
	}
	if !hasPermission {
		return errors.Errorf("add search permission failed, current permission: %b", permFull)
	}
	return nil
}

func SearchKeyring(id string) (string, error) {
	sessKeyID, err := GetSessionID()
	if err != nil {
		return "", err
	}

	key, err := unix.KeyctlSearch(sessKeyID, "user", id, 0)
	if err != nil {
		return "", errors.Wrapf(err, "searck key %s in session keyring %d", id, sessKeyID)
	}

	return getData(key)
}

func getData(key int) (string, error) {
	size := 512
	buffer := make([]byte, size)

	for {
		sizeRead, err := unix.KeyctlBuffer(unix.KEYCTL_READ, key, buffer, size)
		if err != nil {
			return "", err
		}

		if sizeRead < size {
			return string(buffer[:sizeRead]), nil
		}

		size += 512
		buffer = make([]byte, size)
	}
}
