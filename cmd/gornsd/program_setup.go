// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"path/filepath"

	"github.com/gmlewis/go-reticulum/rns"
)

func programSetup(logger *rns.Logger, configDir string, verbosity, quietness int, service bool) (*rns.Reticulum, error) {
	if logger == nil {
		logger = rns.NewLogger()
	}

	if service {
		logger.SetLogDest(rns.LogDestFile)
		logger.SetLogFilePath(filepath.Join(configDir, "logfile"))
	}

	ts := rns.NewTransportSystem()
	ret, err := rns.NewReticulumWithLogger(ts, configDir, logger)
	if err != nil {
		return nil, err
	}

	if !service {
		adjustedLevel := logger.GetLogLevel() + verbosity - quietness
		if adjustedLevel < 0 {
			adjustedLevel = 0
		}
		if adjustedLevel > 7 {
			adjustedLevel = 7
		}
		logger.SetLogLevel(adjustedLevel)
	}

	if ret.IsConnectedToSharedInstance() {
		logger.Log(fmt.Sprintf("Started gornsd version %v connected to another shared local instance, this is probably NOT what you want!", rns.VERSION), rns.LogWarning, false)
	} else {
		logger.Log(fmt.Sprintf("Started gornsd version %v", rns.VERSION), rns.LogNotice, false)
	}

	return ret, nil
}
