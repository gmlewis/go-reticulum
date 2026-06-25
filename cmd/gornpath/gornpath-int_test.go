// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

func buildGornpath(t *testing.T) string {
	t.Helper()
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	bin := filepath.Join(tmpDir, "gornpath")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build gornpath: %v\n%v", err, string(out))
	}
	return bin
}

func TestIntegration_NoArgsPrintsUsageAndExitsZero(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	bin := buildGornpath(t)
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("gornpath with no args failed: %v\n%v", err, string(out))
	}
	if !strings.Contains(string(out), "usage: gornpath") {
		t.Fatalf("missing usage text: %q", string(out))
	}
}

func TestIntegration_VersionPrintsProgramVersion(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	bin := buildGornpath(t)
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("gornpath --version failed: %v\n%v", err, string(out))
	}
	if !strings.Contains(string(out), "gornpath ") {
		t.Fatalf("missing version output: %q", string(out))
	}
}

func TestIntegration_InvalidFlagExitsTwoAndPrintsUsage(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	bin := buildGornpath(t)
	out, err := exec.Command(bin, "--does-not-exist").CombinedOutput()
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

func TestIntegrationRenderPathTable(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)

	paths := []rns.PathInfo{{
		Hash:      []byte{0x01},
		Timestamp: time.Date(2026, 4, 5, 15, 0, 0, 0, time.UTC),
		NextHop:   []byte{0x11},
		Hops:      1,
		Interface: pathTableTestInterface{name: "eth0"},
		Expires:   time.Date(2026, 4, 5, 15, 7, 36, 0, time.UTC),
	}, {
		Hash:      []byte{0x02},
		Timestamp: time.Date(2026, 4, 5, 15, 1, 0, 0, time.UTC),
		NextHop:   []byte{0x22},
		Hops:      2,
		Interface: pathTableTestInterface{name: "eth1"},
		Expires:   time.Date(2026, 4, 5, 15, 8, 36, 0, time.UTC),
	}}

	got, err := renderPathTable(paths, 0, false, nil)
	if err != nil {
		t.Fatalf("renderPathTable returned error: %v", err)
	}
	want := "01 is 1 hop  away via 11 on eth0 expires 2026-04-05 15:07:36\n02 is 2 hops away via 22 on eth1 expires 2026-04-05 15:08:36\n"
	if got != want {
		t.Fatalf("renderPathTable mismatch:\nwant:\n%sgot:\n%s", want, got)
	}
}

func TestIntegrationRenderRateTable(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)

	now := time.Date(2026, 4, 5, 15, 0, 0, 0, time.UTC)
	rows := []any{
		map[string]any{
			"hash":            []byte{0x01},
			"last":            float64(now.Add(-2 * time.Minute).Unix()),
			"rate_violations": 0,
			"blocked_until":   float64(0),
			"timestamps":      []any{float64(now.Add(-3 * time.Hour).Unix())},
		},
	}

	got, err := renderRateTable(rows, now, nil, false)
	if err != nil {
		t.Fatalf("renderRateTable returned error: %v", err)
	}
	want := "01 last heard 2 minutes ago, 0.333 announces/hour in the last 3 hours\n"
	if got != want {
		t.Fatalf("renderRateTable mismatch:\nwant:\n%sgot:\n%s", want, got)
	}
}

func TestIntegrationRenderBlackholedIdentities(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)

	now := time.Date(2026, 4, 5, 15, 0, 0, 0, time.UTC)
	rows := []any{
		map[string]any{
			"identity_hash": []byte{0x01},
			"until":         int64(0),
			"reason":        "Announce spam",
			"source":        []byte{0x09, 0x09, 0x09},
		},
	}

	got, err := renderBlackholedIdentities(rows, now, "", nil)
	if err != nil {
		t.Fatalf("renderBlackholedIdentities returned error: %v", err)
	}
	want := "<01> blackholed indefinitely (Announce spam) by <090909>\n"
	if strings.TrimSpace(got) != strings.TrimSpace(want) {
		t.Fatalf("renderBlackholedIdentities mismatch:\nwant:\n%sgot:\n%s", want, got)
	}
}

type integrationPathRequestFake struct {
	requested bool
	path      *rns.PathInfo
}

func (f *integrationPathRequestFake) HasPath([]byte) bool {
	return f.requested
}

func (f *integrationPathRequestFake) RequestPath([]byte) error {
	f.requested = true
	f.path = &rns.PathInfo{
		Hash:      []byte{0xaa, 0xbb},
		NextHop:   []byte{0xcc, 0xdd},
		Hops:      2,
		Interface: pathRequestInterface{},
		Expires:   time.Date(2026, 4, 5, 15, 7, 36, 0, time.UTC),
	}
	return nil
}

func (f *integrationPathRequestFake) GetPathEntry([]byte) *rns.PathInfo {
	return f.path
}

func TestIntegrationDoRequest(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)

	fake := &integrationPathRequestFake{}
	var out bytes.Buffer
	if err := doRequestAt(&out, fake, []byte{0xaa, 0xbb}, 1.0, func() time.Time { return time.Unix(0, 0) }, func(time.Duration) {}); err != nil {
		t.Fatalf("doRequestAt returned error: %v", err)
	}
	want := "Path to aabb requested  \rPath found, destination aabb is 2 hops away via ccdd on eth0\n"
	if out.String() != want {
		t.Fatalf("doRequestAt mismatch:\nwant:\n%sgot:\n%s", want, out.String())
	}
}

func TestIntegrationBlackholeRoundTrip(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)

	fake := &blackholeFake{entries: map[string]map[string]any{}}
	var out bytes.Buffer
	if err := doBlackhole(&out, fake, []byte{0x01, 0x02}, 0, "integration-test"); err != nil {
		t.Fatalf("doBlackhole returned error: %v", err)
	}
	if got, want := out.String(), "Blackholed identity <0102>\n"; got != want {
		t.Fatalf("doBlackhole output mismatch: got %q want %q", got, want)
	}

	out.Reset()
	if err := doUnblackhole(&out, fake, []byte{0x01, 0x02}); err != nil {
		t.Fatalf("doUnblackhole returned error: %v", err)
	}
	if got, want := out.String(), "Lifted blackhole for identity <0102>\n"; got != want {
		t.Fatalf("doUnblackhole output mismatch: got %q want %q", got, want)
	}
}

type integrationDropperFake struct {
	dropPathResult bool
	dropViaCount   int
}

func (f *integrationDropperFake) InvalidatePath([]byte) bool {
	return f.dropPathResult
}

func (f *integrationDropperFake) InvalidatePathsViaNextHop([]byte) int {
	return f.dropViaCount
}

func TestIntegrationDropOperations(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)

	fake := &integrationDropperFake{dropPathResult: true, dropViaCount: 2}
	var out bytes.Buffer
	if err := doDrop(&out, fake, []byte{0xaa, 0xbb}); err != nil {
		t.Fatalf("doDrop returned error: %v", err)
	}
	if got, want := out.String(), "Dropped path to aabb\n"; got != want {
		t.Fatalf("doDrop output mismatch: got %q want %q", got, want)
	}

	out.Reset()
	if err := doDropVia(&out, fake, []byte{0xcc, 0xdd}); err != nil {
		t.Fatalf("doDropVia returned error: %v", err)
	}
	if got, want := out.String(), "Dropped all paths via ccdd\n"; got != want {
		t.Fatalf("doDropVia output mismatch: got %q want %q", got, want)
	}
}

func TestFormatParityPrettyDate(t *testing.T) {
	pyDir := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR")
	if pyDir == "" {
		pyDir = filepath.Join(os.Getenv("HOME"), "src", "github.com", "markqvist", "Reticulum")
	}
	if _, err := os.Stat(pyDir); err != nil {
		t.Skipf("original Reticulum repo not found at %v: %v", pyDir, err)
	}

	now := time.Unix(1_700_000_000, 0).UTC()
	nowPy := now.Unix()

	offsets := []int{
		0, 5, 9, 10, 30, 59, 60, 61, 119, 120, 121, 3599, 3600, 3601, 7199, 7200, 7201,
		86399, 86400, 86401, 172800, 259200, 604800, 1209600, 2592000, 7776000, 31536000,
	}

	pyScript := fmt.Sprintf(`
import sys, json
from datetime import datetime, timezone
now = datetime.fromtimestamp(%d, tz=timezone.utc)
values = json.loads(sys.argv[1])
results = {}
for offset in values:
    past = datetime.fromtimestamp(%d - offset, tz=timezone.utc)
    diff = now - past
    second_diff = diff.seconds
    day_diff = diff.days
    if day_diff < 0:
        results[str(offset)] = ""
    elif day_diff == 0:
        if second_diff < 10:
            results[str(offset)] = str(second_diff) + " seconds"
        elif second_diff < 60:
            results[str(offset)] = str(second_diff) + " seconds"
        elif second_diff < 120:
            results[str(offset)] = "1 minute"
        elif second_diff < 3600:
            results[str(offset)] = str(int(second_diff / 60)) + " minutes"
        elif second_diff < 7200:
            results[str(offset)] = "an hour"
        elif second_diff < 86400:
            results[str(offset)] = str(int(second_diff / 3600)) + " hours"
    elif day_diff == 1:
        results[str(offset)] = "1 day"
    elif day_diff < 7:
        results[str(offset)] = str(day_diff) + " days"
    elif day_diff < 31:
        results[str(offset)] = str(int(day_diff / 7)) + " weeks"
    elif day_diff < 365:
        results[str(offset)] = str(int(day_diff / 30)) + " months"
    else:
        results[str(offset)] = str(int(day_diff / 365)) + " years"
print(json.dumps(results))
`, nowPy, nowPy)

	tmpDir := testutils.TempDir(t, "pretty-date-parity-")

	scriptPath := filepath.Join(tmpDir, "pd.py")
	if err := os.WriteFile(scriptPath, []byte(pyScript), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	offsetJSON, _ := json.Marshal(offsets)

	cmd := exec.Command("python3", scriptPath, string(offsetJSON))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python3 failed: %v\n%s", err, out)
	}

	var pyResults map[string]string
	if err := json.Unmarshal(out, &pyResults); err != nil {
		t.Fatalf("json unmarshal: %v\nraw: %s", err, out)
	}

	for _, offset := range offsets {
		key := fmt.Sprintf("%d", offset)
		pyWant, ok := pyResults[key]
		if !ok {
			t.Errorf("no Python result for key %q", key)
			continue
		}
		then := now.Add(-time.Duration(offset) * time.Second)
		goGot := prettyDateAt(now, then)
		if goGot != pyWant {
			t.Errorf("prettyDateAt(now, now-%ds) = %q, want %q (Python)", offset, goGot, pyWant)
		}
	}
}
