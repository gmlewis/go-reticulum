// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
)

func splitProbeFullName(fullName string) (string, []string) {
	parts := strings.Split(fullName, ".")
	appName := ""
	if len(parts) > 0 {
		appName = parts[0]
	}
	var aspects []string
	if len(parts) > 1 {
		aspects = append(aspects, parts[1:]...)
	}
	return appName, aspects
}

func parseProbeDestinationHash(destHex string) ([]byte, error) {
	destLen := (rns.TruncatedHashLength / 8) * 2
	if len(destHex) != destLen {
		return nil, fmt.Errorf("Destination length is invalid, must be %v hexadecimal characters (%v bytes).", destLen, destLen/2)
	}
	hash, err := hex.DecodeString(destHex)
	if err != nil {
		return nil, fmt.Errorf("Invalid destination entered. Check your input.")
	}
	return hash, nil
}

func probeTimeoutSeconds(timeout float64, firstHopTimeout int) float64 {
	if timeout != 0 {
		return timeout
	}
	return DefaultTimeout + float64(firstHopTimeout)
}
