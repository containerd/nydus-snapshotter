/*
   Copyright The nydus Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package manager

import (
	"reflect"
	"testing"

	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/stretchr/testify/assert"
)

func TestDaemonStatesCache(t *testing.T) {
	states := newDaemonStates()

	d1 := &daemon.Daemon{ID: "d1", SnapshotID: "sn1"}
	d2 := &daemon.Daemon{ID: "d2", SnapshotID: "sn2"}

	states.Add(d1)
	states.Add(d2)

	anotherD1 := states.GetByDaemonID("d1", nil)
	anotherD2 := states.GetByDaemonID("d2", nil)

	assert.Equal(t, anotherD1, d1)
	assert.Equal(t, anotherD2, d2)

	assert.True(t, reflect.DeepEqual(states.List(), []*daemon.Daemon{d1, d2}))

	assert.Equal(t, states.Size(), 2)

	states.Remove(d1)

	assert.Equal(t, states.Size(), 1)

	states.RemoveBySnapshotID("sn2")

	assert.Equal(t, states.Size(), 0)

	states.RecoverDaemonState(d2)

	assert.Equal(t, states.Size(), 1)

	states.RemoveBySnapshotID("sn2")
	assert.Equal(t, states.Size(), 0)
}
