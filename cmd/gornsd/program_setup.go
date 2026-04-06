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

func programSetup(configDir string, verbosity, quietness int, service bool) (*rns.Reticulum, error) {
	if service {
		rns.SetLogDest(rns.LogDestFile)
		rns.SetLogFilePath(filepath.Join(configDir, "logfile"))
	}

	ts := rns.NewTransportSystem()
	ret, err := rns.NewReticulum(ts, configDir)
	if err != nil {
		return nil, err
	}

	if !service {
		adjustedLevel := rns.GetLogLevel() + verbosity - quietness
		if adjustedLevel < 0 {
			adjustedLevel = 0
		}
		if adjustedLevel > 7 {
			adjustedLevel = 7
		}
		rns.SetLogLevel(adjustedLevel)
	}

	if ret.IsConnectedToSharedInstance() {
		rns.Log(fmt.Sprintf("Started gornsd version %v connected to another shared local instance, this is probably NOT what you want!", rns.VERSION), rns.LogWarning, false)
	} else {
		rns.Log(fmt.Sprintf("Started gornsd version %v", rns.VERSION), rns.LogNotice, false)
	}

	return ret, nil
}
