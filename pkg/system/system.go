/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package system

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/gorilla/mux"
	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/filesystem"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	metrics "github.com/containerd/nydus-snapshotter/pkg/metrics/tool"
	"github.com/containerd/nydus-snapshotter/pkg/prefetch"
)

const (
	// NOTE: Below service endpoints are still experimental.

	endpointDaemons string = "/api/v1/daemons"
	// Retrieve daemons' persisted states in boltdb. Because the db file is always locked,
	// it's very helpful to check daemon's record in database.
	endpointDaemonRecords  string = "/api/v1/daemons/records"
	endpointDaemonsUpgrade string = "/api/v1/daemons/upgrade"
	endpointPrefetch       string = "/api/v1/prefetch"
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
	fs       *filesystem.Filesystem
	managers []*manager.Manager
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
	ID                    string  `json:"id"`
	Pid                   int     `json:"pid"`
	APISock               string  `json:"api_socket"`
	SupervisorPath        string  `json:"supervisor_path"`
	Reference             int     `json:"reference"`
	HostMountpoint        string  `json:"mountpoint"`
	StartupCPUUtilization float64 `json:"startup_cpu_utilization"`
	MemoryRSS             float64 `json:"memory_rss_kb"`
	ReadData              float32 `json:"read_data_kb"`

	Instances map[string]rafsInstanceInfo `json:"instances"`
}

type rafsInstanceInfo struct {
	SnapshotID  string `json:"snapshot_id"`
	SnapshotDir string `json:"snapshot_dir"`
	Mountpoint  string `json:"mountpoint"`
	ImageID     string `json:"image_id"`
}

func NewSystemController(fs *filesystem.Filesystem, managers []*manager.Manager, sock string) (*Controller, error) {
	if err := os.MkdirAll(filepath.Dir(sock), os.ModePerm); err != nil {
		return nil, err
	}

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
		fs:       fs,
		managers: managers,
		addr:     addr,
		router:   mux.NewRouter(),
	}

	sc.registerRouter()

	return &sc, nil
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

func (sc *Controller) registerRouter() {
	sc.router.HandleFunc(endpointDaemons, sc.describeDaemons()).Methods(http.MethodGet)
	sc.router.HandleFunc(endpointDaemonsUpgrade, sc.upgradeDaemons()).Methods(http.MethodPut)
	sc.router.HandleFunc(endpointDaemonRecords, sc.getDaemonRecords()).Methods(http.MethodGet)
	sc.router.HandleFunc(endpointPrefetch, sc.setPrefetchConfiguration()).Methods(http.MethodPut)
}

func (sc *Controller) setPrefetchConfiguration() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.L.Errorf("Failed to read prefetch list: %v", err)
			return
		}
		if err = prefetch.Pm.SetPrefetchFiles(body); err != nil {
			log.L.Errorf("Failed to parse request body: %v", err)
			return
		}
	}
}

func (sc *Controller) describeDaemons() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		info := make([]daemonInfo, 0, 10)

		for _, manager := range sc.managers {
			daemons := manager.ListDaemons()

			for _, d := range daemons {
				instances := make(map[string]rafsInstanceInfo)
				for _, i := range d.RafsCache.List() {
					instances[i.SnapshotID] = rafsInstanceInfo{
						SnapshotID:  i.SnapshotID,
						SnapshotDir: i.SnapshotDir,
						Mountpoint:  i.GetMountpoint(),
						ImageID:     i.ImageID,
					}
				}

				memRSS, err := metrics.GetProcessMemoryRSSKiloBytes(d.Pid())
				if err != nil {
					log.L.Warnf("Failed to get daemon %s RSS memory", d.ID())
				}

				var readData float32
				fsMetrics, err := d.GetFsMetrics("")
				if err != nil {
					log.L.Warnf("Failed to get file system metrics")
				} else {
					readData = float32(fsMetrics.DataRead) / 1024
				}

				i := daemonInfo{
					ID:                    d.ID(),
					Pid:                   d.Pid(),
					HostMountpoint:        d.HostMountpoint(),
					Reference:             int(d.GetRef()),
					Instances:             instances,
					StartupCPUUtilization: d.StartupCPUUtilization,
					MemoryRSS:             memRSS,
					ReadData:              readData,
				}

				info = append(info, i)
			}
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

		for _, manager := range sc.managers {
			manager.Lock()
			defer manager.Unlock()

			daemons := manager.ListDaemons()

			// TODO: Keep the nydusd executive path in Daemon state and persis it since nydusd
			// can run on both versions.
			// Create a dedicated directory storing nydusd of various versions?
			// TODO: daemon client has a method to query daemon version and information.
			for _, d := range daemons {
				err = sc.upgradeNydusDaemon(d, c, manager)
				if err != nil {
					log.L.Errorf("Upgrade daemon %s failed, %s", d.ID(), err)
					statusCode = http.StatusInternalServerError
					return
				}
			}

			// TODO: why renaming?
			err = os.Rename(c.NydusdPath, manager.NydusdBinaryPath)
			if err != nil {
				log.L.Errorf("Rename nydusd binary from %s to  %s failed, %v",
					c.NydusdPath, manager.NydusdBinaryPath, err)
				statusCode = http.StatusInternalServerError
				return
			}
		}
	}
}

// Provide minimal parameters since most of it can be recovered by nydusd states.
// Create a new daemon in Manger to take over the service.
func (sc *Controller) upgradeNydusDaemon(d *daemon.Daemon, c upgradeRequest, manager *manager.Manager) error {
	log.L.Infof("Upgrading nydusd %s, request %v", d.ID(), c)

	fs := sc.fs

	var new daemon.Daemon
	new.States = d.States
	new.Supervisor = d.Supervisor
	new.CloneRafsInstances(d)

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

	su := manager.SupervisorSet.GetSupervisor(d.ID())
	if err := su.SendStatesTimeout(time.Second * 10); err != nil {
		return errors.Wrap(err, "Send states")
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

	fs.TryRetainSharedDaemon(&new)

	if err := new.Start(); err != nil {
		return errors.Wrap(err, "start file system service")
	}

	if err := manager.SubscribeDaemonEvent(&new); err != nil {
		return &json.InvalidUnmarshalError{}
	}

	log.L.Infof("Started service of upgraded daemon on socket %s", new.GetAPISock())

	if err := manager.UpdateDaemonLocked(&new); err != nil {
		return err
	}

	log.L.Infof("Upgraded daemon success on socket %s", new.GetAPISock())

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
