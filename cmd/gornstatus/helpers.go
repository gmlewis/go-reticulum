// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"math"
	"strings"
)

// Interface mode constants matching Python's Interface.MODE_* values.
const (
	modeFull         = 0x01
	modePointToPoint = 0x02
	modeAccessPoint  = 0x03
	modeRoaming      = 0x04
	modeBoundary     = 0x05
	modeGateway      = 0x06
)

// modeString returns the human-readable mode name for a given mode
// constant, matching Python rnstatus.py output.
func modeString(mode int) string {
	switch mode {
	case modeAccessPoint:
		return "Access Point"
	case modePointToPoint:
		return "Point-to-Point"
	case modeRoaming:
		return "Roaming"
	case modeBoundary:
		return "Boundary"
	case modeGateway:
		return "Gateway"
	default:
		return "Full"
	}
}

// clientsString returns the formatted clients/serving/peers line
// for a given interface, matching Python rnstatus.py output.
func clientsString(name string, clients *int) string {
	if clients == nil {
		return ""
	}
	c := *clients
	if strings.HasPrefix(name, "Shared Instance[") {
		cnum := c - 1
		if cnum < 0 {
			cnum = 0
		}
		spec := " programs"
		if cnum == 1 {
			spec = " program"
		}
		return fmt.Sprintf("Serving   : %v%v", cnum, spec)
	}
	if strings.HasPrefix(name, "I2PInterface[") {
		spec := " connected I2P endpoints"
		if c == 1 {
			spec = " connected I2P endpoint"
		}
		return fmt.Sprintf("Peers     : %v%v", c, spec)
	}
	return fmt.Sprintf("Clients   : %v", c)
}

// speedStr formats a bitrate value into a human-readable string with
// units like "bps", "kbps", "Mbps", etc. Note that the kilo prefix
// is lowercase 'k' to match the Python rnstatus.py convention.
func speedStr(num float64) string {
	units := []string{"", "k", "M", "G", "T", "P", "E", "Z"}
	lastUnit := "Y"
	suffix := "bps"

	for _, unit := range units {
		if math.Abs(num) < 1000.0 {
			return fmt.Sprintf("%3.2f %v%v", num, unit, suffix)
		}
		num /= 1000.0
	}

	return fmt.Sprintf("%.2f %v%v", num, lastUnit, suffix)
}
