// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux || darwin

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const recoveryPythonEnvVar = "GORNODECONF_RECOVERY_PYTHON"

var recoveryPythonModuleProbe = `import importlib.util, sys; sys.exit(0 if all(importlib.util.find_spec(m) for m in sys.argv[1:]) else 1)`

func defaultResolveRecoveryPython() (string, error) {
	if override := strings.TrimSpace(os.Getenv(recoveryPythonEnvVar)); override != "" {
		if err := requireRecoveryPythonModules(override); err != nil {
			return "", fmt.Errorf("%v=%q is not usable: %w", recoveryPythonEnvVar, override, err)
		}
		return override, nil
	}

	for _, candidate := range recoveryPythonCandidates() {
		if err := requireRecoveryPythonModules(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no usable Python interpreter with pyserial found for recovery helper; set %v, activate a virtualenv, or install pyserial for python3", recoveryPythonEnvVar)
}

func recoveryPythonCandidates() []string {
	var candidates []string
	if venv := strings.TrimSpace(os.Getenv("VIRTUAL_ENV")); venv != "" {
		candidates = append(candidates, filepath.Join(venv, "bin", "python"))
	}
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, path)
		}
	}
	if pipxPython := pipxPyserialPythonPath(); pipxPython != "" {
		candidates = append(candidates, pipxPython)
	}
	return uniqueStrings(candidates)
}

func pipxPyserialPythonPath() string {
	if pipx, err := exec.LookPath("pipx"); err == nil {
		out, err := exec.Command(pipx, "environment", "--value", "PIPX_LOCAL_VENVS").CombinedOutput()
		if err == nil {
			candidate := filepath.Join(strings.TrimSpace(string(out)), "pyserial", "bin", "python")
			if isExecutableFile(candidate) {
				return candidate
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, base := range []string{
		filepath.Join(home, ".local", "pipx", "venvs"),
		filepath.Join(home, ".local", "share", "pipx", "venvs"),
		filepath.Join(home, "Library", "Application Support", "pipx", "venvs"),
	} {
		candidate := filepath.Join(base, "pyserial", "bin", "python")
		if isExecutableFile(candidate) {
			return candidate
		}
	}
	return ""
}

func requireRecoveryPythonModules(python string) error {
	cmd := exec.Command(python, append([]string{"-c", recoveryPythonModuleProbe}, "serial", "serial.tools.list_ports")...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return err
	}
	return fmt.Errorf("%w: %v", err, trimmed)
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

func uniqueStrings(values []string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (rt cliRuntime) prepareRecoveryEsptoolCommand(args []string) (string, []string, error) {
	if len(args) == 0 {
		return "", nil, errors.New("missing recovery helper command")
	}
	if filepath.Base(args[0]) != recoveryEsptoolFilename {
		return args[0], args[1:], nil
	}
	if rt.resolveRecoveryPython == nil {
		return args[0], args[1:], nil
	}
	python, err := rt.resolveRecoveryPython()
	if err != nil {
		return "", nil, err
	}
	return python, args, nil
}
