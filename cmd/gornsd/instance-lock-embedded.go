// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build embedded || pocket_communicator || pocket_hub

package main

import (
	"os"
)

// lockFileExclusive is a no-op on embedded platforms where OS flock is unavailable.
func lockFileExclusive(f *os.File) (bool, error) {
	return true, nil
}
