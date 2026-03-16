// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"math"
)

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
