// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestDefaultResolveRecoveryPythonUsesOverride(t *testing.T) {
	python := fakeRecoveryPython(t, 0)
	t.Setenv(recoveryPythonEnvVar, python)

	got, err := defaultResolveRecoveryPython()
	if err != nil {
		t.Fatalf("defaultResolveRecoveryPython returned error: %v", err)
	}
	if got != python {
		t.Fatalf("defaultResolveRecoveryPython() = %q, want %q", got, python)
	}
}

func TestDefaultResolveRecoveryPythonRejectsBrokenOverride(t *testing.T) {
	python := fakeRecoveryPython(t, 1)
	t.Setenv(recoveryPythonEnvVar, python)

	_, err := defaultResolveRecoveryPython()
	if err == nil {
		t.Fatal("expected defaultResolveRecoveryPython to fail")
	}
	if !strings.Contains(err.Error(), recoveryPythonEnvVar) {
		t.Fatalf("expected override error to mention %v, got %v", recoveryPythonEnvVar, err)
	}
}

func TestDefaultResolveRecoveryPythonFindsVirtualenvPython(t *testing.T) {
	dir, cleanup := testutils.TempDir(t, "gornodeconf-recovery-python-*")
	t.Cleanup(cleanup)
	venvBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(venvBin, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", venvBin, err)
	}
	python := filepath.Join(venvBin, "python")
	writeFakeRecoveryPython(t, python, 0)

	t.Setenv(recoveryPythonEnvVar, "")
	t.Setenv("VIRTUAL_ENV", dir)
	t.Setenv("PATH", dir)

	got, err := defaultResolveRecoveryPython()
	if err != nil {
		t.Fatalf("defaultResolveRecoveryPython returned error: %v", err)
	}
	if got != python {
		t.Fatalf("defaultResolveRecoveryPython() = %q, want %q", got, python)
	}
}

func TestDefaultResolveRecoveryPythonFindsPipxPyserial(t *testing.T) {
	dir, cleanup := testutils.TempDir(t, "gornodeconf-recovery-python-*")
	t.Cleanup(cleanup)

	venvBase := filepath.Join(dir, "venvs")
	python := filepath.Join(venvBase, "pyserial", "bin", "python")
	if err := os.MkdirAll(filepath.Dir(python), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(python), err)
	}
	writeFakeRecoveryPython(t, python, 0)

	pipxPath := filepath.Join(dir, "pipx")
	pipxScript := "#!/bin/sh\n" +
		"if [ \"$1\" = \"environment\" ] && [ \"$2\" = \"--value\" ] && [ \"$3\" = \"PIPX_LOCAL_VENVS\" ]; then\n" +
		"  printf '%s\\n' \"" + venvBase + "\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(pipxPath, []byte(pipxScript), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", pipxPath, err)
	}

	t.Setenv(recoveryPythonEnvVar, "")
	t.Setenv("VIRTUAL_ENV", "")
	t.Setenv("PATH", dir)

	got, err := defaultResolveRecoveryPython()
	if err != nil {
		t.Fatalf("defaultResolveRecoveryPython returned error: %v", err)
	}
	if got != python {
		t.Fatalf("defaultResolveRecoveryPython() = %q, want %q", got, python)
	}
}

func TestPrepareRecoveryEsptoolCommandWrapsHelper(t *testing.T) {
	t.Parallel()

	rt := cliRuntime{
		resolveRecoveryPython: func() (string, error) {
			return "/tmp/fake-python", nil
		},
	}

	name, args, err := rt.prepareRecoveryEsptoolCommand([]string{"/tmp/recovery_esptool.py", "--chip", "auto"})
	if err != nil {
		t.Fatalf("prepareRecoveryEsptoolCommand returned error: %v", err)
	}
	if name != "/tmp/fake-python" {
		t.Fatalf("command name = %q, want %q", name, "/tmp/fake-python")
	}
	want := []string{"/tmp/recovery_esptool.py", "--chip", "auto"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("command args = %#v, want %#v", args, want)
	}
}

func TestPrepareRecoveryEsptoolCommandLeavesDirectCommandUntouched(t *testing.T) {
	t.Parallel()

	rt := cliRuntime{
		resolveRecoveryPython: func() (string, error) {
			return "/tmp/fake-python", nil
		},
	}

	name, args, err := rt.prepareRecoveryEsptoolCommand([]string{"avrdude", "-p", "m1284p"})
	if err != nil {
		t.Fatalf("prepareRecoveryEsptoolCommand returned error: %v", err)
	}
	if name != "avrdude" {
		t.Fatalf("command name = %q, want %q", name, "avrdude")
	}
	want := []string{"-p", "m1284p"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("command args = %#v, want %#v", args, want)
	}
}

func fakeRecoveryPython(t *testing.T, exitCode int) string {
	t.Helper()

	dir, cleanup := testutils.TempDir(t, "gornodeconf-fake-python-*")
	t.Cleanup(cleanup)
	path := filepath.Join(dir, "python3")
	writeFakeRecoveryPython(t, path, exitCode)
	return path
}

func writeFakeRecoveryPython(t *testing.T, path string, exitCode int) {
	t.Helper()

	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"-c\" ]; then\n" +
		"  exit " + string(rune('0'+exitCode)) + "\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
