/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package pprof

import (
	"net"
	"net/http"
	"net/http/pprof"

	"github.com/containerd/log"
	"github.com/pkg/errors"
)

func NewPprofHTTPListener(addr string) error {
	if addr == "" {
		return errors.New("the address for pprof HTTP server is invalid")
	}

	http.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
	http.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	http.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
	http.Handle("/debug/pprof/block", pprof.Handler("block"))
	http.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
	http.Handle("/debug/pprof/heap", pprof.Handler("heap"))

	l, err := net.Listen("tcp", addr)
	if err != nil {
		return errors.Wrapf(err, "pprof server listener, addr=%s", addr)
	}

	go func() {
		log.L.Infof("Start pprof HTTP server on %s", addr)

		if err := http.Serve(l, nil); err != nil && !errors.Is(err, net.ErrClosed) {
			log.L.Errorf("Pprof server fails to listen or serve %s: %v", addr, err)
		}
	}()

	return nil
}
