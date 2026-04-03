// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"

	"github.com/gmlewis/go-reticulum/rns"
)

func parseDestinationHash(destination string) ([]byte, error) {
	destLen := (rns.TruncatedHashLength / 8) * 2
	if len(destination) != destLen {
		return nil, fmt.Errorf("Allowed destination length is invalid, must be %v hexadecimal characters (%v bytes).", destLen, destLen/2)
	}

	destinationHash, err := rns.HexToBytes(destination)
	if err != nil {
		return nil, fmt.Errorf("Invalid destination entered. Check your input.")
	}

	return destinationHash, nil
}
