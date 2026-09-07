// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build embedded || pocket_communicator || pocket_hub

package rns

import (
	"errors"
)

// runDiscoverySubprocess is a stub on embedded platforms where subprocesses are not supported.
func runDiscoverySubprocess(execPath string) ([]byte, error) {
	return nil, errors.New("subprocesses are not supported on embedded targets")
}
