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
	if !app.service {
		logger.SetPendingDelta(app.verbose - app.quiet)
	}
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
// (e.g. "127.0.0.1:6062"). It is strictly opt-in via the -pprof-addr flag:
// when addr is empty it is a no-op with zero overhead. It mirrors the
// gonomadnet/gorrcd pprof endpoints so the shared instance is inspectable
// exactly like the fleet's applications.
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
