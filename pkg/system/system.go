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

	"github.com/containerd/log"
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
	// Provide backend information
	endpointGetBackend string = "/api/v1/daemons/{id}/backend"
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
	sc.router.HandleFunc(endpointGetBackend, sc.getBackend()).Methods(http.MethodGet)
}

func (sc *Controller) getBackend() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var err error
		var statusCode int

		defer func() {
			if err != nil {
				m := newErrorMessage(err.Error())
				http.Error(w, m.encode(), statusCode)
			}
		}()

		vars := mux.Vars(r)
		id := vars["id"]

		for _, ma := range sc.managers {
			ma.Lock()
			d := ma.GetByDaemonID(id)

			if d != nil {
				backendType, backendConfig := d.Config.StorageBackend()
				backend := struct {
					BackendType string      `json:"type"`
					Config      interface{} `json:"config"`
				}{
					backendType,
					backendConfig,
				}
				jsonResponse(w, backend)
				ma.Unlock()
				return
			}
			ma.Unlock()
		}

		err = errdefs.ErrNotFound
		statusCode = http.StatusNotFound
	}
}

func (sc *Controller) setPrefetchConfiguration() func(w http.ResponseWriter, r *http.Request) {
	return func(_ http.ResponseWriter, r *http.Request) {
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
	return func(w http.ResponseWriter, _ *http.Request) {
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
	return func(w http.ResponseWriter, _ *http.Request) {
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

			sourcePath := c.NydusdPath
			destinationPath := manager.NydusdBinaryPath

			if err = copyNydusdBinary(sourcePath, destinationPath); err != nil {
				log.L.Errorf("Failed to copy nydusd binary from %s to %s: %v",
					sourcePath, destinationPath, err)
				statusCode = http.StatusInternalServerError
				return
			}

			log.L.Infof("Successfully copy nydusd binary from %s to %s",
				sourcePath, destinationPath)
		}
	}
}

// Provide minimal parameters since most of it can be recovered by nydusd states.
// Create a new daemon in Manger to take over the service.
func (sc *Controller) upgradeNydusDaemon(d *daemon.Daemon, c upgradeRequest, manager *manager.Manager) error {
	supervisor := d.Supervisor
	if supervisor == nil {
		return errors.New("should set recover policy to failover to enable hot upgrade")
	}

	log.L.Infof("Upgrading nydusd %s, request %v", d.ID(), c)

	fs := sc.fs

	newDaemon := daemon.Daemon{
		States:     d.States,
		Supervisor: supervisor,
	}
	newDaemon.CloneRafsInstances(d)

	s := path.Base(d.GetAPISock())
	next, err := buildNextAPISocket(s)
	if err != nil {
		return err
	}

	upgradingSocket := path.Join(path.Dir(d.GetAPISock()), next)
	newDaemon.States.APISocket = upgradingSocket

	cmd, err := manager.BuildDaemonCommand(&newDaemon, c.NydusdPath, true)
	if err != nil {
		return err
	}

	if err := supervisor.SendStatesTimeout(time.Second * 10); err != nil {
		return errors.Wrap(err, "Send states")
	}

	if err := cmd.Start(); err != nil {
		return errors.Wrap(err, "start process")
	}

	newDaemon.States.ProcessID = cmd.Process.Pid

	if err := newDaemon.WaitUntilState(types.DaemonStateInit); err != nil {
		return errors.Wrap(err, "wait until init state")
	}

	if err := newDaemon.TakeOver(); err != nil {
		return errors.Wrap(err, "take over resources")
	}

	if err := newDaemon.WaitUntilState(types.DaemonStateReady); err != nil {
		return errors.Wrap(err, "wait unit ready state")
	}

	if err := manager.UnsubscribeDaemonEvent(d); err != nil {
		return errors.Wrap(err, "unsubscribe daemon event")
	}

	// Let the older daemon exit without umount
	if err := d.Exit(); err != nil {
		return errors.Wrap(err, "old daemon exits")
	}

	fs.TryRetainSharedDaemon(&newDaemon)

	if err := newDaemon.Start(); err != nil {
		return errors.Wrap(err, "start file system service")
	}

	if err := manager.SubscribeDaemonEvent(&newDaemon); err != nil {
		return &json.InvalidUnmarshalError{}
	}

	log.L.Infof("Started service of upgraded daemon on socket %s", newDaemon.GetAPISock())

	if err := manager.UpdateDaemonLocked(&newDaemon); err != nil {
		return err
	}

	log.L.Infof("Upgraded daemon success on socket %s", newDaemon.GetAPISock())

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

// copyNydusdBinary copies a file from sourcePath to destinationPath,
// ensuring parent directories exist and setting 0755 permissions.
// It overwrites the destination file if it already exists.
func copyNydusdBinary(sourcePath, destinationPath string) error {
	fileMode := os.FileMode(0755)

	destDir := filepath.Dir(destinationPath)
	if err := os.MkdirAll(destDir, fileMode); err != nil {
		return fmt.Errorf("failed to create destination directory %s: %w", destDir, err)
	}

	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to open source file %s: %w", sourcePath, err)
	}
	defer sourceFile.Close()

	destinationFile, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %w", destinationPath, err)
	}
	defer destinationFile.Close()

	if _, err := io.Copy(destinationFile, sourceFile); err != nil {
		return fmt.Errorf("failed to copy file contents from %s to %s: %w", sourcePath, destinationPath, err)
	}

	if err := os.Chmod(destinationPath, fileMode); err != nil {
		return fmt.Errorf("failed to set permissions for %s to %s: %w", destinationPath, fileMode.String(), err)
	}

	return nil
}
