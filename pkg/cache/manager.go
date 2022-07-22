package cache

import (
	"os"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/store"
	"github.com/pkg/errors"
)

type Manager struct {
	db       DB
	store    *Store
	cacheDir string
	period   time.Duration
	eventCh  chan struct{}
	fsDriver string
}

type Opt struct {
	CacheDir string
	Period   time.Duration
	Database *store.Database
	FsDriver string
}

func NewManager(opt Opt) (*Manager, error) {
	// Ensure cache directory exists
	if err := os.MkdirAll(opt.CacheDir, 0755); err != nil {
		return nil, errors.Wrapf(err, "failed to create cache dir %s", opt.CacheDir)
	}

	db, err := store.NewCacheStore(opt.Database)
	if err != nil {
		return nil, err
	}
	s := NewStore(opt.CacheDir)

	eventCh := make(chan struct{})
	m := &Manager{
		db:       db,
		store:    s,
		cacheDir: opt.CacheDir,
		period:   opt.Period,
		eventCh:  eventCh,
		fsDriver: opt.FsDriver,
	}

	// For fscache backend, the cache is maintained by the kernel fscache module,
	// so here we ignore gc for now, and in the future we need another design
	// to remove the cache.
	if opt.FsDriver == config.FsDriverFscache {
		return m, nil
	}

	go m.runGC()
	log.L.Info("gc goroutine start...")

	return m, nil
}

func (m *Manager) CacheDir() string {
	return m.cacheDir
}

func (m *Manager) SchedGC() {
	if m.fsDriver == config.FsDriverFscache {
		return
	}
	m.eventCh <- struct{}{}
}

func (m *Manager) runGC() {
	tick := time.NewTicker(m.period)
	defer tick.Stop()
	for {
		select {
		case <-m.eventCh:
			if err := m.gc(); err != nil {
				log.L.Infof("[event] cache gc err, %v", err)
			}
			tick.Reset(m.period)
		case <-tick.C:
			if err := m.gc(); err != nil {
				log.L.Infof("[tick] cache gc err, %v", err)
			}
		}
	}
}

func (m *Manager) gc() error {
	delBlobs, err := m.db.GC(m.store.DelBlob)
	if err != nil {
		return errors.Wrapf(err, "cache gc err")
	}
	log.L.Debugf("remove %d unused blobs successfully", len(delBlobs))
	return nil
}

func (m *Manager) AddSnapshot(imageID string, blobs []string) error {
	return m.db.AddSnapshot(imageID, blobs)
}

func (m *Manager) DelSnapshot(imageID string) error {
	return m.db.DelSnapshot(imageID)
}
