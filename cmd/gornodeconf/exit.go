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

type exitController struct {
	activeRNode  rnodeLeaver
	activeSerial serialPort
	sleep        func(time.Duration)
	exit         func(int)
}

func newExitController() *exitController {
	return &exitController{sleep: time.Sleep, exit: os.Exit}
}

func (c *exitController) gracefulExit(code int) {
	if c.activeRNode != nil {
		c.activeRNode.Leave()
		c.activeRNode = nil
	} else if c.activeSerial != nil {
		c.sleep(time.Second)
		_ = c.activeSerial.Close()
		c.activeSerial = nil
	}

	c.exit(code)
}
