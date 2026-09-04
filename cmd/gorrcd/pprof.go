// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"log"
	"net"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof/* on http.DefaultServeMux
)

// startPProf starts an HTTP server exposing net/http/pprof endpoints at addr
// (e.g. "127.0.0.1:6061"). It is strictly opt-in via the -pprof-addr flag:
// when addr is empty it is a no-op with zero overhead (no goroutine, no
// listener, no pprof sampling). It mirrors gonomadnet's -pprof-addr so the
// live hub can be inspected (goroutine dumps, CPU/heap profiles) exactly like
// the fleet's clients:
//
//	gorrcd -pprof-addr 127.0.0.1:6061
//	curl http://127.0.0.1:6061/debug/pprof/goroutine?debug=2
func startPProf(addr string) {
	if addr == "" {
		return
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("pprof: listen %s: %v", addr, err)
		return
	}
	go func() {
		log.Printf("pprof: serving on http://%s/debug/pprof/", ln.Addr())
		if err := http.Serve(ln, nil); err != nil {
			log.Printf("pprof: serve %s: %v", addr, err)
		}
	}()
}
