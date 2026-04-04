// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"time"
)

type rnodeLeaver interface {
	Leave()
}

var (
	activeRNode  rnodeLeaver
	activeSerial serialPort
	sleepFunc    = time.Sleep
	exitFunc     = os.Exit
)

func gracefulExit(code int) {
	if activeRNode != nil {
		activeRNode.Leave()
		activeRNode = nil
	} else if activeSerial != nil {
		sleepFunc(time.Second)
		_ = activeSerial.Close()
		activeSerial = nil
	}

	exitFunc(code)
}
