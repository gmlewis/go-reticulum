// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
)

func formatProbeMTUError(rawLen int) string {
	return fmt.Sprintf("Error: Probe packet size of %v bytes exceed MTU of %v bytes", rawLen, 500)
}

func formatProbeSentLine(sent, size int, destHash []byte, more string) string {
	return fmt.Sprintf("\rSent probe %v (%v bytes) to %v%v  ", sent, size, rns.PrettyHex(destHash), more)
}

func formatProbeRTTString(rttSeconds float64) string {
	rounded := 0.0
	units := "seconds"
	if rttSeconds >= 1.0 {
		rounded = math.Round(rttSeconds*1000) / 1000
	} else {
		rounded = math.Round(rttSeconds*1000000) / 1000
		units = "milliseconds"
	}
	value := strconv.FormatFloat(rounded, 'f', -1, 64)
	if !strings.Contains(value, ".") {
		value += ".0"
	}
	return value + " " + units
}

func formatProbeHopSuffix(hops int) string {
	if hops == 1 {
		return ""
	}
	return "s"
}

func formatProbeReplyLine(destHash []byte, rttSeconds float64, hops int, receptionStats string) string {
	return fmt.Sprintf("Valid reply from %v\nRound-trip time is %v over %v hop%v%v\n", rns.PrettyHex(destHash), formatProbeRTTString(rttSeconds), hops, formatProbeHopSuffix(hops), receptionStats)
}
