// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import "fmt"

type rnodeDetectWriter interface {
	Write([]byte) (int, error)
}

func rnodeDetectCommand() []byte {
	return []byte{
		kissFend, 0x08, 0x73, kissFend,
		kissFend, 0x50, 0x00, kissFend,
		kissFend, 0x48, 0x00, kissFend,
		kissFend, 0x49, 0x00, kissFend,
		kissFend, 0x47, 0x00, kissFend,
		kissFend, 0x56, 0x01, kissFend,
		kissFend, 0x60, 0x01, kissFend,
		kissFend, 0x60, 0x02, kissFend,
	}
}

func rnodeDetect(port rnodeDetectWriter, name string) error {
	command := rnodeDetectCommand()
	written, err := port.Write(command)
	if err != nil {
		return err
	}
	if written != len(command) {
		return fmt.Errorf("An IO error occurred while detecting hardware for %v", name)
	}
	return nil
}
