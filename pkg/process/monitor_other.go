//go:build !linux
// +build !linux

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

package process

// LivenessMonitor liveness of a nydusd daemon.
type LivenessMonitor interface {
	// Subscribe death event of a nydusd daemon.
	// `path` is where the monitor is listening on.
	Subscribe(id string, path string, notifier chan<- deathEvent) error
	// Unsubscribe death event of a nydusd daemon.
	Unsubscribe(id string) error
	// Run the monitor, wait for nydusd death event.
	Run()
	// Stop the monitor and release all the resources.
	Destroy()
}

type livenessMonitor struct {
}

type deathEvent struct {
	daemonID string
	path     string
}

func newMonitor() (*livenessMonitor, error) {
	return &livenessMonitor{}, nil
}

func (m *livenessMonitor) Subscribe(id string, path string, notifier chan<- deathEvent) (err error) {
	panic("unimplemented")
}

func (m *livenessMonitor) Unsubscribe(id string) (err error) { panic("unimplemented") }

func (m *livenessMonitor) Run() { panic("unimplemented") }

func (m *livenessMonitor) Destroy() { panic("unimplemented") }
