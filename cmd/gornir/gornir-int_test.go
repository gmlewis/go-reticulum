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

func findRnir(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("rnir")
	if err != nil {
		t.Skip("rnir not found in PATH, skipping Python/Go parity test")
	}
	return path
}

func TestIntegration_VersionOutput(t *testing.T) {
	t.Parallel()
	bin := buildGornir(t)
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("gornir --version failed: %v\n%v", err, string(out))
	}
	want := "gornir " + rns.VERSION
	got := strings.TrimSpace(string(out))
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestIntegration_ExampleConfigOutput(t *testing.T) {
	t.Parallel()
	bin := buildGornir(t)
	out, err := exec.Command(bin, "--exampleconfig").CombinedOutput()
	if err != nil {
		t.Fatalf("gornir --exampleconfig failed: %v\n%v", err, string(out))
	}
	output := string(out)
	for _, want := range []string{
		"example Reticulum config file",
		"[reticulum]",
		"enable_transport",
		"[logging]",
		"[interfaces]",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("exampleconfig output missing %q", want)
		}
	}
}

func TestIntegration_ExitCodeZero(t *testing.T) {
	t.Parallel()
	bin := buildGornir(t)
	tmpDir := tempDir(t)
	cmd := exec.Command(bin, "--config", tmpDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gornir exited with error: %v\n%v", err, string(out))
	}
}

func TestIntegration_HelpOutput(t *testing.T) {
	t.Parallel()
	bin := buildGornir(t)
	out, _ := exec.Command(bin, "--help").CombinedOutput()
	output := string(out)
	for _, want := range []string{
		"Reticulum Distributed Identity Resolver",
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
	rnirBin := findRnir(t)
	gornirBin := buildGornir(t)

	pyOut, err := exec.Command(rnirBin, "--exampleconfig").CombinedOutput()
	if err != nil {
		// rnir.py has a bug: __example_rns_config__ is undefined
		// so --exampleconfig will crash with NameError. Skip if so.
		if strings.Contains(string(pyOut), "NameError") {
			t.Skip("rnir --exampleconfig fails due to Python bug (undefined __example_rns_config__)")
		}
		t.Fatalf("rnir --exampleconfig failed: %v\n%v", err, string(pyOut))
	}
	goOut, err := exec.Command(gornirBin, "--exampleconfig").CombinedOutput()
	if err != nil {
		t.Fatalf("gornir --exampleconfig failed: %v\n%v", err, string(goOut))
	}

	pyTrimmed := strings.TrimSpace(string(pyOut))
	goTrimmed := strings.TrimSpace(string(goOut))
	if pyTrimmed != goTrimmed {
		t.Errorf("exampleconfig output differs between Python and Go")
	}
}

func TestParity_HelpFlags(t *testing.T) {
	t.Parallel()
	rnirBin := findRnir(t)
	gornirBin := buildGornir(t)

	pyOut, _ := exec.Command(rnirBin, "--help").CombinedOutput()
	goOut, _ := exec.Command(gornirBin, "--help").CombinedOutput()

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
