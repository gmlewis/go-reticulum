// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

func preflightRnodeSerial(port string) (serialPort, error) {
	serial, err := rnodeOpenSerial(port)
	if err != nil {
		return nil, err
	}
	return serial, nil
}
