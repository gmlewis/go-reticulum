// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"os/exec"
	"sync"
	"testing"
)

var (
	pythonNomadnetCandidates = []string{
		"python3",
		"/opt/homebrew/bin/python3",
		"python3.14",
		"python3.13",
	}
	pythonNomadnetExeOnce sync.Once
	pythonNomadnetExePath string
)

func probePythonNomadnetExe() string {
	for _, exe := range pythonNomadnetCandidates {
		cmd := exec.Command(exe, "-c", "import nomadnet.RRC")
		if err := cmd.Run(); err == nil {
			return exe
		}
	}
	return ""
}

func getPythonNomadnetExe() string {
	pythonNomadnetExeOnce.Do(func() {
		pythonNomadnetExePath = probePythonNomadnetExe()
	})
	return pythonNomadnetExePath
}

func skipIfNoPythonNomadnet(t *testing.T) {
	t.Helper()
	if getPythonNomadnetExe() == "" {
		t.Skip("skipping live parity test: no python3 interpreter with nomadnet found")
	}
}
