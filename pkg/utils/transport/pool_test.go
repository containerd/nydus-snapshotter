package transport

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/stretchr/testify/require"
)

type FakeReference struct {
	Tag        string
	Repository name.Repository
}

var _ name.Reference = (*FakeReference)(nil)

func (t FakeReference) Context() name.Repository {
	return t.Repository
}

func (t FakeReference) Identifier() string {
	return t.Tag
}

func (t FakeReference) Name() string {
	return t.Repository.Name() + ":" + t.Tag
}

func (t FakeReference) String() string {
	return t.Name()
}

func (t FakeReference) Scope(action string) string {
	return t.Repository.Scope(action)
}

func TestResolve(t *testing.T) {
	var callCount int
	failed := false
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		if !failed {
			fmt.Fprintf(w, "ok")
			return
		}
		failed = false
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer svr.Close()

	pool := NewPool()
	repo, err := name.NewRepository(strings.TrimPrefix(svr.URL, "http://")+"/nginx", name.WeakValidation, name.Insecure)
	require.NoError(t, err)

	ref := &FakeReference{
		Tag:        "latest",
		Repository: repo,
	}

	url1, tr1, err := pool.Resolve(ref, "fake digest", nil)
	require.NoError(t, err)
	// auth request + redirect request
	require.Equal(t, 2, callCount)
	// reset
	callCount = 0
	url2, tr2, err := pool.Resolve(ref, "fake digest", nil)
	// get transport from pool and call redirect
	require.Equal(t, 1, callCount)
	require.NoError(t, err)
	require.Equal(t, tr1, tr2)
	require.Equal(t, url1, url2)

	failed = true
	// reset
	callCount = 0
	url3, tr3, err := pool.Resolve(ref, "fake digest", nil)
	// redirect failed and retry redirect + auth
	require.Equal(t, 3, callCount)
	require.NoError(t, err)
	require.Equal(t, tr2, tr3)
	require.Equal(t, url2, url3)

}
