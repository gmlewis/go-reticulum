// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import "fmt"

type rnodeDetectWriter interface {
	Write([]byte) (int, error)
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
