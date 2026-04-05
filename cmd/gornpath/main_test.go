// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

func TestGornpathNoArgsPrintsUsageAndExitsZero(t *testing.T) {
	t.Parallel()

	out, err := runGornpath()
	if err != nil {
		t.Fatalf("gornpath with no args failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "usage: gornpath") {
		t.Fatalf("missing usage text: %q", out)
	}
}

func TestGornpathVersionPrintsProgramVersion(t *testing.T) {
	t.Parallel()

	out, err := runGornpath("--version")
	if err != nil {
		t.Fatalf("gornpath --version failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "gornpath ") {
		t.Fatalf("missing version output: %q", out)
	}
}

func TestGornpathInvalidFlagExitsTwoAndPrintsUsage(t *testing.T) {
	t.Parallel()

	tmpDir := tempDir(t)
	binPath := filepath.Join(tmpDir, "gornpath")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	buildCmd.Dir = "."
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build gornpath: %v\n%v", err, string(out))
	}

	cmd := exec.Command(binPath, "--does-not-exist")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected parse failure, got success")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if got, want := exitErr.ExitCode(), 2; got != want {
		t.Fatalf("exit code = %v, want %v", got, want)
	}
	if !strings.Contains(string(out), "usage: gornpath") {
		t.Fatalf("missing usage output: %v", string(out))
	}
}

func TestDoTableUsesRenderer(t *testing.T) {
	t.Parallel()

	paths := []rns.PathInfo{{
		Hash:      []byte{0x01},
		NextHop:   []byte{0x11},
		Hops:      1,
		Interface: pathTableTestInterface{name: "eth0"},
		Expires:   time.Date(2026, 4, 5, 14, 30, 46, 0, time.UTC),
	}}

	var out bytes.Buffer
	if err := doTable(&out, pathTableProviderFunc(func() []rns.PathInfo { return paths }), 0, false); err != nil {
		t.Fatalf("doTable returned error: %v", err)
	}
	if got, want := out.String(), "01 is 1 hop  away via 11 on eth0 expires 2026-04-05 14:30:46\n"; got != want {
		t.Fatalf("table output mismatch: got %q want %q", got, want)
	}
}

type pathTableProviderFunc func() []rns.PathInfo

func (f pathTableProviderFunc) GetPathTable() []rns.PathInfo {
	return f()
}

var _ pathTableProvider = pathTableProviderFunc(nil)

func TestDoTableJSONUsesRenderer(t *testing.T) {
	t.Parallel()

	paths := []rns.PathInfo{{
		Timestamp: time.Unix(123, 0),
		Hash:      []byte{0xaa, 0xbb},
		NextHop:   []byte{0xcc, 0xdd},
		Hops:      2,
		Interface: pathTableTestInterface{name: "eth0"},
		Expires:   time.Unix(456, 0),
	}}

	var out bytes.Buffer
	if err := doTable(&out, pathTableProviderFunc(func() []rns.PathInfo { return paths }), 0, true); err != nil {
		t.Fatalf("doTable returned error: %v", err)
	}
	if got, want := out.String(), "[{\"hash\":\"aabb\",\"timestamp\":123,\"via\":\"ccdd\",\"hops\":2,\"expires\":456,\"interface\":\"eth0\"}]"; got != want {
		t.Fatalf("table JSON mismatch: got %q want %q", got, want)
	}
}

func runGornpath(args ...string) (string, error) {
	cmdArgs := append([]string{"run", "."}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func tempDir(t *testing.T) string {
	t.Helper()
	baseDir := ""
	if runtime.GOOS == "darwin" {
		baseDir = "/tmp"
	}
	dir, err := os.MkdirTemp(baseDir, "gornpath-test-")
	if err != nil {
		t.Fatalf("tempDir error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}
