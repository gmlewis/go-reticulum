// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

func findRnpkg(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("rnpkg")
	if err != nil {
		t.Skip("rnpkg not found in PATH, skipping Python/Go parity test")
	}
	return path
}

func TestIntegration_VersionOutput(t *testing.T) {
	t.Parallel()
	bin, cleanup := buildGornpkg(t)
	defer cleanup()
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("gornpkg --version failed: %v\n%v", err, string(out))
	}
	want := "gornpkg " + rns.VERSION
	got := strings.TrimSpace(string(out))
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestIntegration_ExampleConfigOutput(t *testing.T) {
	t.Parallel()
	bin, cleanup := buildGornpkg(t)
	defer cleanup()
	out, err := exec.Command(bin, "--exampleconfig").CombinedOutput()
	if err != nil {
		t.Fatalf("gornpkg --exampleconfig failed: %v\n%v", err, string(out))
	}
	output := string(out)
	want := "# This is an example package manager configuration file.\n"
	if output != want {
		t.Errorf("exampleconfig output = %q, want %q", output, want)
	}
}

func TestIntegration_ExitCodeZero(t *testing.T) {
	t.Parallel()
	bin, cleanup := buildGornpkg(t)
	defer cleanup()
	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	cmd := exec.Command(bin, "--config", tmpDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gornpkg exited with error: %v\n%v", err, string(out))
	}
}

func TestIntegration_HelpOutput(t *testing.T) {
	t.Parallel()
	bin, cleanup := buildGornpkg(t)
	defer cleanup()
	out, _ := exec.Command(bin, "--help").CombinedOutput()
	output := string(out)
	for _, want := range []string{
		"Reticulum Meta Package Manager",
		"--config",
		"-v, --verbose",
		"-q, --quiet",
		"--exampleconfig",
		"--version",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("help output missing %q, got:\n%v", want, output)
		}
	}
}

func TestParity_ExampleConfig(t *testing.T) {
	t.Parallel()
	rnpkgBin := findRnpkg(t)
	gornpkgBin, cleanup := buildGornpkg(t)
	defer cleanup()

	pyOut, err := exec.Command(rnpkgBin, "--exampleconfig").CombinedOutput()
	if err != nil {
		t.Fatalf("rnpkg --exampleconfig failed: %v\n%v", err, string(pyOut))
	}
	goOut, err := exec.Command(gornpkgBin, "--exampleconfig").CombinedOutput()
	if err != nil {
		t.Fatalf("gornpkg --exampleconfig failed: %v\n%v", err, string(goOut))
	}

	pyTrimmed := strings.TrimSpace(string(pyOut))
	goTrimmed := strings.TrimSpace(string(goOut))
	if pyTrimmed != goTrimmed {
		t.Errorf("exampleconfig output differs:\nPython: %q\nGo:     %q", pyTrimmed, goTrimmed)
	}
}

func TestParity_HelpFlags(t *testing.T) {
	t.Parallel()
	rnpkgBin := findRnpkg(t)
	gornpkgBin, cleanup := buildGornpkg(t)
	defer cleanup()

	pyOut, _ := exec.Command(rnpkgBin, "--help").CombinedOutput()
	goOut, _ := exec.Command(gornpkgBin, "--help").CombinedOutput()

	pyStr := string(pyOut)
	goStr := string(goOut)

	for _, flag := range []string{"--config", "--verbose", "--quiet", "--exampleconfig", "--version"} {
		if !strings.Contains(pyStr, flag) {
			t.Logf("note: Python help missing %q (may be expected)", flag)
		}
		if !strings.Contains(goStr, flag) {
			t.Errorf("Go help missing %q", flag)
		}
	}
}
