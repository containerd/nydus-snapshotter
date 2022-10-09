/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package system

import (
	"encoding/json"
	"net"
	"net/http"
	"os"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	"github.com/gorilla/mux"
	"github.com/pkg/errors"
)

const (
	// NOTE: Below service endpoints are still experimental.

	endpointDaemons string = "/api/v1/daemons"
	// Retrieve daemons' persisted states in boltdb. Because the db file is always locked,
	// it's very helpful to check daemon's record in database.
	endpointDaemonRecords  string = "/api/v1/daemons/records"
	endpointDaemonsUpgrade string = "/api/v1/daemons/upgrade"
)

// Nydus-snapshotter might manage dozens of running nydus daemons, each daemon may have multiple
// file system instances attached. For easy maintenance, the system controller can interact with
// all the daemons in a consistent and automatic way.

// 1. Get all daemons status and information
// 2. Trigger all daemons to restart and reload configuration
// 3. Rolling update
// 4. Daemons failures record as metrics
type Controller struct {
	manager *manager.Manager
	// httpSever *http.Server
	addr   *net.UnixAddr
	router *mux.Router
}

type upgradeRequest struct {
	NydusdPath string `json:"nydusd_path"`
	Version    string `json:"version"`
	Policy     string `json:"policy"`
}

type daemonInfo struct {
	ID         string `json:"id"`
	SnapshotID string `json:"snapshot_id"`
	Pid        int    `json:"pid"`
	ImageID    string `json:"image_id"`
	APISock    string `json:"api_socket"`
	// TODO: trim me, only for trace and analyze purpose at this stage
	RootMountPoint   string
	CustomMountPoint string
	SupervisorPath   string
	SnapshotDir      string
}

func NewSystemController(manager *manager.Manager, sock string) (*Controller, error) {
	if err := os.Remove(sock); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	}

	addr, err := net.ResolveUnixAddr("unix", sock)
	if err != nil {
		return nil, errors.Wrapf(err, "resolve address %s", sock)
	}

	sc := Controller{
		manager: manager,
		addr:    addr,
		router:  mux.NewRouter(),
	}

	sc.registerRouter()

	return &sc, nil
}

func (sc *Controller) registerRouter() {
	sc.router.HandleFunc(endpointDaemons, sc.describeDaemons()).Methods(http.MethodGet)
	sc.router.HandleFunc(endpointDaemonsUpgrade, sc.upgradeDaemons()).Methods(http.MethodPost)
	sc.router.HandleFunc(endpointDaemonRecords, sc.getDaemonRecords()).Methods(http.MethodGet)
}

func (sc *Controller) describeDaemons() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		daemons := sc.manager.ListDaemons()
		log.L.Infof("list daemons %v", daemons)

		info := make([]daemonInfo, 0, 10)

		for _, d := range daemons {
			RootMountPointGetter := func() string {
				if d.RootMountPoint != nil {
					return *d.RootMountPoint
				}
				return ""
			}

			CustomMountPointGetter := func() string {
				if d.CustomMountPoint != nil {
					return *d.CustomMountPoint
				}
				return ""
			}

			i := daemonInfo{ID: d.ID,
				SnapshotID:       d.SnapshotID,
				Pid:              d.Pid,
				ImageID:          d.ImageID,
				RootMountPoint:   RootMountPointGetter(),
				CustomMountPoint: CustomMountPointGetter(),
				SnapshotDir:      d.SnapshotDir}

			info = append(info, i)
		}

		respBody, err := json.Marshal(&info)
		if err != nil {
			log.L.Errorf("marshal error, %s", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		w.Write(respBody)
	}
}

// TODO: Implement me!
func (sc *Controller) getDaemonRecords() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		daemons := sc.manager.ListDaemons()
		log.L.Infof("list daemons %v", daemons)
	}
}

// POST /api/v1/nydusd/upgrade
// body: {"nydusd_path": "/path/to/new/nydusd", "version": "v2.2.1", "policy": "rolling"}
// Possible policy: rolling, immediate
// Live upgrade procedure:
//  1. Check if new version of nydusd executive is existed.
//  2. Validate its version matching `version` in this request.
//  3. Upgrade one nydusd:
//     a. Lock the whole manager daemons cache, no daemon can be inserted of deleted from manager
//     b. Start a new nydusd with `--upgrade` flag, wait until it reaches INTI state
//     c. Validate the new nydusd's version returned by API /daemon
//     d. Send resources like FD and daemon running states to the new nydusd by API /takeover
//     e. Wait until new nydusd reaches state READY
//     f. Command the old nydusd to exit
//     g. Send API /start to the new nydusd making it take over the whole file system service
//
// 4. Upgrade next nydusd like step 3.
// 5. If upgrading a certain nydusd fails, abort!
// 6. Delete the old nydusd executive
func (sc *Controller) upgradeDaemons() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		daemons := sc.manager.ListDaemons()

		var c upgradeRequest

		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			log.L.Errorf("request %v, decode error %s", r, err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// TODO: Keep the nydusd executive path in Daemon state and persis it since nydusd
		// can run on both versions.
		// Create a dedicated directory storing nydusd of various versions?

		// TODO: Let struct `Daemon` have a method to start a process rather than forking
		// process in daemons manager
		// TODO: daemon client has a method to query daemon version and information.
		for _, d := range daemons {
			if err := upgradeNydusDaemon(d, c); err != nil {
				log.L.Errorf("Upgrade daemon %s failed, %s", d.ID, err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
}

func (sc *Controller) Run() error {
	log.L.Infof("Start system controller API server on %s", sc.addr)
	listener, err := net.ListenUnix("unix", sc.addr)
	if err != nil {
		return errors.Wrapf(err, "listen to socket %s ", sc.addr)
	}

	err = http.Serve(listener, sc.router)
	if err != nil {
		return errors.Wrapf(err, "system management serving")
	}

	return nil
}

// TODO: Implement me!
// New API socket path
// Supervisor path does not need to be changed
// Provide minimal parameters since most of it can be recovered by nydusd states
func upgradeNydusDaemon(d *daemon.Daemon, c upgradeRequest) error {
	log.L.Infof("Upgrading nydusd %s, request %v", d.ID, c)
	return errdefs.ErrNotImplemented
}
