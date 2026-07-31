// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func nextBootstrapSerialNumber(configDir string) (uint32, error) {
	counterPath := filepath.Join(configDir, "firmware", "serial.counter")
	var counter uint32
	if data, err := os.ReadFile(counterPath); err == nil {
		parsed, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr != nil {
			return 0, fmt.Errorf("could not create device serial number, exiting")
		}
		counter = uint32(parsed)
	} else if !os.IsNotExist(err) {
		return 0, fmt.Errorf("could not create device serial number, exiting")
	}

	serialno := counter + 1
	if err := os.MkdirAll(filepath.Dir(counterPath), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(counterPath, []byte(strconv.FormatUint(uint64(serialno), 10)), 0o644); err != nil {
		return 0, fmt.Errorf("could not create device serial number, exiting")
	}
	return serialno, nil
}
