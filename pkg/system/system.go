/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package system

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/exporter"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/registry"
)

const (
	// NOTE: Below service endpoints are still experimental.

	endpointDaemons string = "/api/v1/daemons"
	// Retrieve daemons' persisted states in boltdb. Because the db file is always locked,
	// it's very helpful to check daemon's record in database.
	endpointDaemonRecords  string = "/api/v1/daemons/records"
	endpointDaemonsUpgrade string = "/api/v1/daemons/upgrade"

	// Export prometheus metrics
	endpointPromMetrics string = "/metrics"
)

const defaultErrorCode string = "Unknown"

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

type errorMessage struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func newErrorMessage(message string) errorMessage {
	return errorMessage{Code: defaultErrorCode, Message: message}
}

func (m *errorMessage) encode() string {
	msg, err := json.Marshal(&m)
	if err != nil {
		log.L.Errorf("Failed to encode error message, %s", err)
		return ""
	}
	return string(msg)
}

func jsonResponse(w http.ResponseWriter, payload interface{}) {
	respBody, err := json.Marshal(&payload)
	if err != nil {
		log.L.Errorf("marshal error, %s", err)
		m := newErrorMessage(err.Error())
		http.Error(w, m.encode(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(respBody); err != nil {
		log.L.Errorf("write body %s", err)
	}
}

type daemonInfo struct {
	ID             string `json:"id"`
	Pid            int    `json:"pid"`
	APISock        string `json:"api_socket"`
	SupervisorPath string
	Reference      int    `json:"reference"`
	HostMountpoint string `json:"mountpoint"`

	Instances map[string]rafsInstanceInfo `json:"instances"`
}

type rafsInstanceInfo struct {
	SnapshotID  string `json:"snapshot_id"`
	SnapshotDir string `json:"snapshot_dir"`
	Mountpoint  string `json:"mountpoint"`
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
	sc.router.HandleFunc(endpointDaemonsUpgrade, sc.upgradeDaemons()).Methods(http.MethodPut)
	sc.router.HandleFunc(endpointDaemonRecords, sc.getDaemonRecords()).Methods(http.MethodGet)

	// Special registration for Prometheus metrics export
	sc.RegisterMetricsHandler(endpointPromMetrics)
}

func (sc *Controller) RegisterMetricsHandler(endpoint string) {
	handler := promhttp.HandlerFor(registry.Registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.HTTPErrorOnError,
	})

	sc.router.Handle(endpoint,
		http.HandlerFunc(func(rsp http.ResponseWriter, req *http.Request) {
			handler.ServeHTTP(rsp, req)
			exporter.ExportToFile()
		}))
}

func (sc *Controller) describeDaemons() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		daemons := sc.manager.ListDaemons()
		log.L.Infof("list daemons %v", daemons)

		info := make([]daemonInfo, 0, 10)

		for _, d := range daemons {

			instances := make(map[string]rafsInstanceInfo)
			for _, i := range d.Instances.List() {
				instances[i.SnapshotID] = rafsInstanceInfo{
					SnapshotID:  i.SnapshotID,
					SnapshotDir: i.SnapshotDir,
					Mountpoint:  i.GetMountpoint()}
			}

			i := daemonInfo{
				ID:             d.ID(),
				Pid:            d.Pid(),
				HostMountpoint: d.HostMountpoint(),
				Reference:      int(d.GetRef()),
				Instances:      instances,
			}

			info = append(info, i)
		}

		jsonResponse(w, &info)
	}
}

// TODO: Implement me!
func (sc *Controller) getDaemonRecords() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		m := newErrorMessage("not implemented")
		http.Error(w, m.encode(), http.StatusNotImplemented)
	}
}

// PUT /api/v1/nydusd/upgrade
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
		sc.manager.Lock()
		defer sc.manager.Unlock()

		daemons := sc.manager.ListDaemons()

		var c upgradeRequest
		var err error
		var statusCode int

		defer func() {
			if err != nil {
				m := newErrorMessage(err.Error())
				http.Error(w, m.encode(), statusCode)
			}
		}()

		err = json.NewDecoder(r.Body).Decode(&c)
		if err != nil {
			log.L.Errorf("request %v, decode error %s", r, err)
			statusCode = http.StatusBadRequest
			return
		}

		// TODO: Keep the nydusd executive path in Daemon state and persis it since nydusd
		// can run on both versions.
		// Create a dedicated directory storing nydusd of various versions?
		// TODO: daemon client has a method to query daemon version and information.
		for _, d := range daemons {
			err = sc.upgradeNydusDaemon(d, c)
			if err != nil {
				log.L.Errorf("Upgrade daemon %s failed, %s", d.ID(), err)
				statusCode = http.StatusInternalServerError
				return
			}
		}

		err = os.Rename(c.NydusdPath, sc.manager.NydusdBinaryPath)
		if err != nil {
			log.L.Errorf("Rename nydusd binary from %s to  %s failed, %v",
				c.NydusdPath, sc.manager.NydusdBinaryPath, err)
			statusCode = http.StatusInternalServerError
			return
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

// Provide minimal parameters since most of it can be recovered by nydusd states.
// Create a new daemon in Manger to take over the service.
func (sc *Controller) upgradeNydusDaemon(d *daemon.Daemon, c upgradeRequest) error {
	log.L.Infof("Upgrading nydusd %s, request %v", d.ID(), c)

	manager := sc.manager

	var new daemon.Daemon
	new.States = d.States
	new.CloneInstances(d)

	s := path.Base(d.GetAPISock())
	next, err := buildNextAPISocket(s)
	if err != nil {
		return err
	}

	upgradingSocket := path.Join(path.Dir(d.GetAPISock()), next)
	new.States.APISocket = upgradingSocket

	cmd, err := manager.BuildDaemonCommand(&new, c.NydusdPath, true)
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return errors.Wrap(err, "start process")
	}

	if err := new.WaitUntilState(types.DaemonStateInit); err != nil {
		return errors.Wrap(err, "wait until init state")
	}

	if err := new.TakeOver(); err != nil {
		return errors.Wrap(err, "take over resources")
	}

	if err := new.WaitUntilState(types.DaemonStateReady); err != nil {
		return errors.Wrap(err, "wait unit ready state")
	}

	if err := manager.UnsubscribeDaemonEvent(d); err != nil {
		return errors.Wrap(err, "unsubscribe daemon event")
	}

	// Let the older daemon exit without umount
	if err := d.Exit(); err != nil {
		return errors.Wrap(err, "old daemon exits")
	}

	if err := new.Start(); err != nil {
		return errors.Wrap(err, "start file system service")
	}

	if err := manager.SubscribeDaemonEvent(&new); err != nil {
		return &json.InvalidUnmarshalError{}
	}

	log.L.Infof("Started service of upgraded daemon on socket %s", new.GetAPISock())

	if err := manager.UpdateDaemon(&new); err != nil {
		return err
	}

	return nil
}

// Name next api socket path based on currently api socket path listened on.
// The principle is to add a suffix number to api[0-9]+.sock
func buildNextAPISocket(cur string) (string, error) {
	n := strings.Split(cur, ".")
	if len(n) != 2 {
		return "", errdefs.ErrInvalidArgument
	}
	r := regexp.MustCompile(`[0-9]+`)
	m := r.Find([]byte(n[0]))
	var num int
	if m == nil {
		num = 1
	} else {
		var err error
		num, err = strconv.Atoi(string(m))
		if err != nil {
			return "", err
		}
		num++
	}

	nextSocket := fmt.Sprintf("api%d.sock", num)
	return nextSocket, nil
}
