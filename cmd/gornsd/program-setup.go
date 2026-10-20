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
	"path/filepath"

	"github.com/gmlewis/go-reticulum/rns"
)

func (app *appT) programSetup() (*rns.Reticulum, error) {
	startPProf(app.pprofAddr)
	logger := app.logger
	// Apply the -v/-q verbosity delta in every mode, including service mode,
	// so that -s -v -v raises the file log to transport-level debug.
	logger.SetPendingDelta(app.verbose - app.quiet)
	if app.service {
		logger.SetLogDest(rns.LogDestFile)
		logger.SetLogFilePath(filepath.Join(app.configDir, "logfile"))
	}

	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, app.configDir, logger)
	if err != nil {
		return nil, err
	}

	if ret.IsConnectedToSharedInstance() {
		logger.Warning("Started gornsd version %v connected to another shared local instance, this is probably NOT what you want!", rns.VERSION)
	} else {
		logger.Notice("Started gornsd version %v", rns.VERSION)
	}

	return ret, nil
}

// startPProf starts an HTTP server exposing net/http/pprof endpoints at addr
// (e.g. "127.0.0.1:6062"). It is opt-in via the -pprof-addr flag: an empty
// addr is a no-op with zero overhead.
func startPProf(addr string) {
	if addr == "" {
		return
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("pprof: listen %v: %v", addr, err)
		return
	}
	go func() {
		log.Printf("pprof: serving on http://%s/debug/pprof/", ln.Addr())
		if err := http.Serve(ln, nil); err != nil {
			log.Printf("pprof: serve %v: %v", addr, err)
		}
	}()
}
