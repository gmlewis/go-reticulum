// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"path/filepath"

	"github.com/gmlewis/go-reticulum/rns"
)

func (app *appT) programSetup() (*rns.Reticulum, error) {
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
