// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/hex"
	"fmt"
)

func parseHash(input string) ([]byte, error) {
	const destLen = 32
	if len(input) != destLen {
		return nil, fmt.Errorf("Hash length is invalid, must be %v hexadecimal characters (%v bytes).", destLen, destLen/2)
	}
	decoded, err := hex.DecodeString(input)
	if err != nil {
		return nil, fmt.Errorf("Invalid hash entered. Check your input.")
	}
	return decoded, nil
}
