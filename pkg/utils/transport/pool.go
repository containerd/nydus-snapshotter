package transport

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/utils/registry"
	"github.com/golang/groupcache/lru"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/pkg/errors"
)

var _ Resolve = &Pool{}

const HTTPClientTimeOut = time.Second * 60

// LRU cache for authenticated network connections.
type Pool struct {
	trPoolMu  sync.Mutex
	trPool    *lru.Cache
	transport http.RoundTripper
}

func NewPool() *Pool {
	pool := Pool{
		transport: http.DefaultTransport,
		trPool:    lru.New(3000),
	}
	return &pool
}

type Resolve interface {
	Resolve(ref name.Reference, digest string, keychain authn.Keychain) (string, http.RoundTripper, error)
}

func (r *Pool) Resolve(ref name.Reference, digest string, keychain authn.Keychain) (string, http.RoundTripper, error) {
	r.trPoolMu.Lock()
	defer r.trPoolMu.Unlock()
	endpointURL := fmt.Sprintf("%s://%s/v2/%s/blobs/%s",
		ref.Context().Registry.Scheme(),
		ref.Context().RegistryStr(),
		ref.Context().RepositoryStr(),
		digest)

	if tr, ok := r.trPool.Get(ref.Name()); ok {
		var err error
		if url, err := redirect(endpointURL, tr.(http.RoundTripper)); err == nil {
			return url, tr.(http.RoundTripper), nil
		}
		r.trPool.Remove(ref.Name())
		log.L.Warnf("redirect %s, failed, err: %s", endpointURL, err)
	}
	tr, err := registry.AuthnTransport(ref, r.transport, keychain)
	if err != nil {
		return "", nil, errors.Wrapf(err, "failed to authn transport")
	}
	url, err := redirect(endpointURL, tr)
	if err != nil {
		return "", nil, errors.Wrapf(err, "failed to redirect %s", endpointURL)
	}
	r.trPool.Add(ref.Name(), tr)
	return url, tr, nil
}

func redirect(endpointURL string, tr http.RoundTripper) (url string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), HTTPClientTimeOut)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", endpointURL, nil)
	if err != nil {
		return "", errors.Wrapf(err, "failed to request to the registry")
	}
	req.Close = false
	req.Header.Set("Range", "bytes=0-0")
	res, err := tr.RoundTrip(req)
	if err != nil {
		return "", errors.Wrapf(err, "failed to request to %q", endpointURL)
	}

	defer func() {
		_, err := io.Copy(io.Discard, res.Body)
		if err != nil {
			log.L.Warnf("Discard body failed %s", err)
		}
		res.Body.Close()
	}()

	if res.StatusCode/100 == 2 {
		url = endpointURL
	} else if redir := res.Header.Get("Location"); redir != "" && res.StatusCode/100 == 3 {
		url = redir
	} else {
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return "", errors.Wrapf(err, "failed to get response body")
		}
		return "", fmt.Errorf("failed to access to %q with code %v, body: %s", endpointURL, res.StatusCode, body)
	}
	return
}
