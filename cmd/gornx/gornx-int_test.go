// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

var gornxBinaryPath string

func TestMain(m *testing.M) {
	// This entire suite will be skipped if `-short` is used.
	flag.Parse()
	if testing.Short() {
		os.Exit(0)
	}

	binDir, cleanup := testutils.TempDirMain("gornx-bin-")

	gornxBinaryPath = filepath.Join(binDir, "gornx")
	build := exec.Command("go", "build", "-o", gornxBinaryPath, ".")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		log.Fatalf("failed to build gornx binary: %v\n", err)
	}

	exitCode := m.Run()

	cleanup()
	out, err := exec.Command("/usr/bin/pkill", "-f", binDir).CombinedOutput()
	if err != nil {
		log.Printf("pkill -f %q failed: %v\n%s", binDir, err, out)
	}

	os.Exit(exitCode)
}

func getGornxBinaryPath(t *testing.T) string {
	t.Helper()
	if gornxBinaryPath == "" {
		t.Fatal("gornx binary path not initialized by TestMain")
	}
	return gornxBinaryPath
}

func getRnxPythonBinaryPath(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("rnx")
	if err != nil {
		t.Skip("rnx not found in PATH, skipping Python/Go integration tests")
	}
	return path
}

func TestIntegrationVersionOutput(t *testing.T) {
	t.Parallel()
	gornxBin := getGornxBinaryPath(t)
	out, err := exec.Command(gornxBin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("gornx --version failed: %v\n%v", err, string(out))
	}
	want := "gornx " + rns.VERSION + "\n"
	if got := string(out); got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestIntegrationNoArgs(t *testing.T) {
	t.Parallel()
	gornxBin := getGornxBinaryPath(t)
	out, err := exec.Command(gornxBin).CombinedOutput()
	if err != nil {
		t.Fatalf("gornx with no args failed: %v\n%v", err, string(out))
	}
	got := string(out)
	if !strings.Contains(got, "usage: gornx") {
		t.Errorf("output missing usage line, got:\n%v", got)
	}
}
