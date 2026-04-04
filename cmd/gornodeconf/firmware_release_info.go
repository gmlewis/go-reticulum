// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"strings"
)

func parseFirmwareReleaseInfo(data []byte) (string, string, error) {
	parts := strings.Fields(strings.TrimSpace(string(data)))
	if len(parts) < 2 {
		return "", "", fmt.Errorf("firmware release info is malformed")
	}
	return parts[0], parts[1], nil
}
