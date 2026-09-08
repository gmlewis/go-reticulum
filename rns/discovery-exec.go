// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !embedded && !pocket_communicator && !pocket_hub

package rns

import (
	"errors"
	"os/exec"
)

// runDiscoverySubprocess executes an external command at execPath and returns its output.
func runDiscoverySubprocess(execPath string) ([]byte, error) {
	output, err := exec.Command(execPath).Output()
	if err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			return nil, errors.New("Non-zero exit code from subprocess")
		}
		return nil, err
	}
	return output, nil
}
