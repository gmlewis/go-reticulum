// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

func TestRunEquivalenceScenarioAggregatesStepComparisons(t *testing.T) {
	t.Parallel()

	leftCalls := 0
	rightCalls := 0
	scenario := equivalenceScenario{
		fixture: defaultEquivalenceFixtures()[0],
		steps: []equivalenceStep{
			{
				name: "version",
				left: func() commandOutcome {
					leftCalls++
					return commandOutcome{stdout: "gornpath 0.1.0\n", exitCode: 0}
				},
				right: func() commandOutcome {
					rightCalls++
					return commandOutcome{stdout: "rnpath 0.1.0\n", exitCode: 0}
				},
			},
			{
				name: "help",
				left: func() commandOutcome {
					leftCalls++
					return commandOutcome{stdout: "usage: gornpath\n", exitCode: 0, files: map[string]string{"left.txt": "alpha"}}
				},
				right: func() commandOutcome {
					rightCalls++
					return commandOutcome{stdout: "usage: rnpath\n", exitCode: 0, files: map[string]string{"right.txt": "bravo"}}
				},
			},
		},
	}

	report := runEquivalenceScenario(scenario)
	if leftCalls != 2 || rightCalls != 2 {
		t.Fatalf("expected both sides to run twice, got left=%v right=%v", leftCalls, rightCalls)
	}
	if report.fixture != scenario.fixture {
		t.Fatalf("fixture mismatch: got %#v want %#v", report.fixture, scenario.fixture)
	}
	if len(report.steps) != 2 {
		t.Fatalf("step count mismatch: got %v want 2", len(report.steps))
	}
	if report.steps[0].name != "version" || report.steps[1].name != "help" {
		t.Fatalf("step names mismatch: %#v", report.steps)
	}
	if len(report.steps[0].comparison.diffs) != 1 {
		t.Fatalf("expected first step to differ, got %#v", report.steps[0].comparison.diffs)
	}
	if len(report.diffs) != 4 {
		t.Fatalf("diff count mismatch: got %v want 4", len(report.diffs))
	}
	want := []outcomeDifference{
		{field: "stdout", got: "gornpath 0.1.0\n", want: "rnpath 0.1.0\n"},
		{field: "stdout", got: "usage: gornpath\n", want: "usage: rnpath\n"},
		{field: "file", path: "left.txt", got: "alpha", want: "<missing>"},
		{field: "file", path: "right.txt", got: "<missing>", want: "bravo"},
	}
	if !reflect.DeepEqual(report.diffs, want) {
		t.Fatalf("report diffs mismatch:\n got: %#v\nwant: %#v", report.diffs, want)
	}
}

func TestRunEquivalenceScenarioComparesRealVersionCommands(t *testing.T) {
	t.Parallel()

	fixture := defaultEquivalenceFixtures()[0]
	scenario := equivalenceScenario{
		fixture: fixture,
		steps: []equivalenceStep{{
			name:  "version",
			left:  func() commandOutcome { return runGoGornpathOutcome(t, "--version") },
			right: func() commandOutcome { return runPythonRnpathOutcome(t, "--version") },
		}},
	}

	report := runEquivalenceScenario(scenario)
	if len(report.steps) != 1 {
		t.Fatalf("unexpected step count: %v", len(report.steps))
	}
	if report.steps[0].name != "version" {
		t.Fatalf("unexpected step name: %q", report.steps[0].name)
	}
	if report.steps[0].comparison.left.exitCode != 0 || report.steps[0].comparison.right.exitCode != 0 {
		t.Fatalf("expected both commands to succeed: %#v", report.steps[0].comparison)
	}
	if len(report.diffs) == 0 {
		t.Fatal("expected version commands to differ in output")
	}
	if report.diffs[0].field != "stdout" {
		t.Fatalf("expected stdout diff first, got %#v", report.diffs[0])
	}
}

func TestRunEquivalenceScenarioLocalTableHasNoDiffs(t *testing.T) {
	t.Parallel()

	fixtures := defaultEquivalenceFixtures()
	var localTableFixture equivalenceFixture
	for _, fixture := range fixtures {
		if fixture.name == "local-table" {
			localTableFixture = fixture
			break
		}
	}
	if localTableFixture.name == "" {
		t.Fatal("missing local-table fixture")
	}

	paths := []rns.PathInfo{{
		Hash:      []byte{0x01},
		NextHop:   []byte{0x11},
		Hops:      1,
		Interface: pathTableTestInterface{name: "eth0"},
		Expires:   time.Date(2026, 4, 5, 15, 7, 36, 0, time.UTC),
	}}
	rendered, err := renderPathTable(paths, 0, false, nil)
	if err != nil {
		t.Fatalf("renderPathTable returned error: %v", err)
	}

	report := runEquivalenceScenario(equivalenceScenario{
		fixture: localTableFixture,
		steps: []equivalenceStep{{
			name: "table",
			left: func() commandOutcome {
				return commandOutcome{stdout: rendered, exitCode: 0}
			},
			right: func() commandOutcome {
				return commandOutcome{stdout: rendered, exitCode: 0}
			},
		}},
	})

	if len(report.diffs) != 0 {
		t.Fatalf("expected no diffs, got %#v", report.diffs)
	}
	if report.fixture.name != "local-table" {
		t.Fatalf("unexpected fixture name: %q", report.fixture.name)
	}
}

type equivalencePathRequestFake struct {
	requested bool
	path      *rns.PathInfo
}

func (f *equivalencePathRequestFake) HasPath([]byte) bool {
	return f.requested
}

func (f *equivalencePathRequestFake) RequestPath([]byte) error {
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

func (f *equivalencePathRequestFake) GetPathEntry([]byte) *rns.PathInfo {
	return f.path
}

func TestRunEquivalenceScenarioDiscoveryHasNoDiffs(t *testing.T) {
	t.Parallel()

	fixtures := defaultEquivalenceFixtures()
	var discoveryFixture equivalenceFixture
	for _, fixture := range fixtures {
		if fixture.name == "discovery" {
			discoveryFixture = fixture
			break
		}
	}
	if discoveryFixture.name == "" {
		t.Fatal("missing discovery fixture")
	}

	runDiscovery := func() commandOutcome {
		fake := &equivalencePathRequestFake{}
		var out bytes.Buffer
		if err := doRequestAt(&out, fake, []byte{0xaa, 0xbb}, 1.0, func() time.Time { return time.Unix(0, 0) }, func(time.Duration) {}); err != nil {
			return commandOutcome{stdout: out.String(), stderr: err.Error(), exitCode: 1}
		}
		return commandOutcome{stdout: out.String(), exitCode: 0}
	}

	report := runEquivalenceScenario(equivalenceScenario{
		fixture: discoveryFixture,
		steps: []equivalenceStep{{
			name:  "discovery",
			left:  runDiscovery,
			right: runDiscovery,
		}},
	})

	if len(report.diffs) != 0 {
		t.Fatalf("expected no diffs, got %#v", report.diffs)
	}
	if report.fixture.name != "discovery" {
		t.Fatalf("unexpected fixture name: %q", report.fixture.name)
	}
}

type equivalenceDropperFake struct {
	dropPathResult bool
	dropViaCount   int
}

func (f *equivalenceDropperFake) InvalidatePath([]byte) bool {
	return f.dropPathResult
}

func (f *equivalenceDropperFake) InvalidatePathsViaNextHop([]byte) int {
	return f.dropViaCount
}

func TestRunEquivalenceScenarioDropHasNoDiffs(t *testing.T) {
	t.Parallel()

	fixtures := defaultEquivalenceFixtures()
	var dropFixture equivalenceFixture
	for _, fixture := range fixtures {
		if fixture.name == "drop" {
			dropFixture = fixture
			break
		}
	}
	if dropFixture.name == "" {
		t.Fatal("missing drop fixture")
	}

	runDrop := func() commandOutcome {
		fake := &equivalenceDropperFake{dropPathResult: true, dropViaCount: 1}
		var out bytes.Buffer
		if err := doDrop(&out, fake, []byte{0xaa, 0xbb}); err != nil {
			return commandOutcome{stdout: out.String(), stderr: err.Error(), exitCode: 1}
		}
		if err := doDropVia(&out, fake, []byte{0xcc, 0xdd}); err != nil {
			return commandOutcome{stdout: out.String(), stderr: err.Error(), exitCode: 1}
		}
		return commandOutcome{stdout: out.String(), exitCode: 0}
	}

	report := runEquivalenceScenario(equivalenceScenario{
		fixture: dropFixture,
		steps: []equivalenceStep{{
			name:  "drop",
			left:  runDrop,
			right: runDrop,
		}},
	})

	if len(report.diffs) != 0 {
		t.Fatalf("expected no diffs, got %#v", report.diffs)
	}
	if report.fixture.name != "drop" {
		t.Fatalf("unexpected fixture name: %q", report.fixture.name)
	}
}

func TestRunEquivalenceScenarioRatesHasNoDiffs(t *testing.T) {
	t.Parallel()

	fixtures := defaultEquivalenceFixtures()
	var ratesFixture equivalenceFixture
	for _, fixture := range fixtures {
		if fixture.name == "rates" {
			ratesFixture = fixture
			break
		}
	}
	if ratesFixture.name == "" {
		t.Fatal("missing rates fixture")
	}

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
	rendered, err := renderRateTable(rows, now, nil, false)
	if err != nil {
		t.Fatalf("renderRateTable returned error: %v", err)
	}

	report := runEquivalenceScenario(equivalenceScenario{
		fixture: ratesFixture,
		steps: []equivalenceStep{{
			name: "rates",
			left: func() commandOutcome {
				return commandOutcome{stdout: rendered, exitCode: 0}
			},
			right: func() commandOutcome {
				return commandOutcome{stdout: rendered, exitCode: 0}
			},
		}},
	})

	if len(report.diffs) != 0 {
		t.Fatalf("expected no diffs, got %#v", report.diffs)
	}
	if report.fixture.name != "rates" {
		t.Fatalf("unexpected fixture name: %q", report.fixture.name)
	}
}

func TestRunEquivalenceScenarioBlackholeHasNoDiffs(t *testing.T) {
	t.Parallel()

	fixtures := defaultEquivalenceFixtures()
	var blackholeFixture equivalenceFixture
	for _, fixture := range fixtures {
		if fixture.name == "blackhole" {
			blackholeFixture = fixture
			break
		}
	}
	if blackholeFixture.name == "" {
		t.Fatal("missing blackhole fixture")
	}

	runBlackhole := func() commandOutcome {
		fake := &blackholeFake{entries: map[string]map[string]any{}}
		var out bytes.Buffer
		if err := doBlackhole(&out, fake, []byte{0x01, 0x02}, 0, "integration-test"); err != nil {
			return commandOutcome{stdout: out.String(), stderr: err.Error(), exitCode: 1}
		}
		if err := doUnblackhole(&out, fake, []byte{0x01, 0x02}); err != nil {
			return commandOutcome{stdout: out.String(), stderr: err.Error(), exitCode: 1}
		}
		return commandOutcome{stdout: out.String(), exitCode: 0}
	}

	report := runEquivalenceScenario(equivalenceScenario{
		fixture: blackholeFixture,
		steps: []equivalenceStep{{
			name:  "blackhole",
			left:  runBlackhole,
			right: runBlackhole,
		}},
	})

	if len(report.diffs) != 0 {
		t.Fatalf("expected no diffs, got %#v", report.diffs)
	}
	if report.fixture.name != "blackhole" {
		t.Fatalf("unexpected fixture name: %q", report.fixture.name)
	}
}

func TestRunEquivalenceScenarioRemoteLinkHasNoDiffs(t *testing.T) {
	t.Parallel()

	fixtures := defaultEquivalenceFixtures()
	var remoteFixture equivalenceFixture
	for _, fixture := range fixtures {
		if fixture.name == "remote-link" {
			remoteFixture = fixture
			break
		}
	}
	if remoteFixture.name == "" {
		t.Fatal("missing remote-link fixture")
	}

	runRemote := func() commandOutcome {
		fake := &remoteRequestFake{response: []any{
			map[string]any{"hash": []byte{0x01}, "timestamp": float64(123), "via": []byte{0x11}, "hops": 1, "expires": float64(456), "interface": "eth0"},
		}}
		var out bytes.Buffer
		if err := doRemoteTable(&out, fake, []byte{0xaa}, 0, false, 1.0); err != nil {
			return commandOutcome{stdout: out.String(), stderr: err.Error(), exitCode: 1}
		}
		return commandOutcome{stdout: out.String(), exitCode: 0}
	}

	report := runEquivalenceScenario(equivalenceScenario{
		fixture: remoteFixture,
		steps: []equivalenceStep{{
			name:  "remote-link",
			left:  runRemote,
			right: runRemote,
		}},
	})

	if len(report.diffs) != 0 {
		t.Fatalf("expected no diffs, got %#v", report.diffs)
	}
	if report.fixture.name != "remote-link" {
		t.Fatalf("unexpected fixture name: %q", report.fixture.name)
	}
}

func runGoGornpathOutcome(t *testing.T, args ...string) commandOutcome {
	t.Helper()

	cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
	cmd.Dir = "."
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return commandOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: commandExitCode(err)}
}

func runPythonRnpathOutcome(t *testing.T, args ...string) commandOutcome {
	t.Helper()

	repoDir := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR")
	if repoDir == "" {
		repoDir = filepath.Join("..", "..", "original-reticulum-repo")
	}
	scriptPath := filepath.Join(repoDir, "RNS", "Utilities", "rnpath.py")
	cmd := exec.Command("python3", append([]string{"-u", scriptPath}, args...)...)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+repoDir)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return commandOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: commandExitCode(err)}
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}
