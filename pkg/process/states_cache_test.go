package process

import (
	"reflect"
	"testing"

	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/stretchr/testify/assert"
)

func TestDaemonStatesCache(t *testing.T) {
	states := newDaemonStates()

	d1 := &daemon.Daemon{ID: "d1"}
	d2 := &daemon.Daemon{ID: "d2"}

	states.Add(d1)
	states.Add(d2)

	another_d1 := states.GetByDaemonID("d1", nil)
	another_d2 := states.GetByDaemonID("d2", nil)

	assert.Equal(t, another_d1, d1)
	assert.Equal(t, another_d2, d2)

	assert.True(t, reflect.DeepEqual(states.List(), []*daemon.Daemon{d1, d2}))
}
