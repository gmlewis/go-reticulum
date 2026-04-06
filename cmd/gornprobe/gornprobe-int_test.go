// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration && linux
// +build integration,linux

package main

import (
	"bytes"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

type probeCommandOutcome struct {
	stdout   string
	stderr   string
	exitCode int
}

type probeIntegrationClock struct {
	now time.Time
}

func (c *probeIntegrationClock) Now() time.Time        { return c.now }
func (c *probeIntegrationClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }

var (
	gornprobeBuildOnce sync.Once
	gornprobeBinPath   string
	gornprobeBuildErr  error
)

func TestGornprobeCLIParity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "help", args: nil},
		{name: "version", args: []string{"--version"}},
		{name: "invalid length", args: []string{"gornprobe.debug", "001122"}},
		{name: "invalid hex", args: []string{"gornprobe.debug", strings.Repeat("z", 32)}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := runGornprobeCommand(t, tc.args...)
			want := runRnprobeCommand(t, tc.args...)
			compareProbeOutcomes(t, got, want)
		})
	}
}

func TestGornprobeKeyboardInterrupt(t *testing.T) {
	t.Parallel()

	binPath := buildGornprobeBinary(t)
	configDir := tempDir(t)
	configText := `[reticulum]
share_instance = No
instance_control_port = 0

[logging]
loglevel = 4

[interfaces]
`
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(configText), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cmd := osexec.Command(binPath, "--config", configDir, "gornprobe.test", "00112233445566778899aabbccddeeff")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start gornprobe: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("failed to signal interrupt: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*osexec.ExitError); ok {
			if got, want := exitErr.ExitCode(), 0; got != want {
				t.Fatalf("exit code = %v, want %v\nstdout: %q\nstderr: %q", got, want, stdout.String(), stderr.String())
			}
		} else {
			t.Fatalf("interrupt wait failed: %v", err)
		}
	}

	if !strings.Contains(stdout.String(), "\n") {
		t.Fatalf("missing interrupt blank line: %q", stdout.String())
	}
}

func TestGornprobeScenarioParity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  func() probeCommandOutcome
		want probeCommandOutcome
	}{
		{
			name: "timeout",
			got:  renderProbeTimeoutScenario,
			want: probeCommandOutcome{
				stdout:   "Sent probe 1 (16 bytes) to <aabb>\nProbe timed out\nSent 1, received 0, packet loss 100.00%\n",
				exitCode: 2,
			},
		},
		{
			name: "success",
			got:  renderProbeSuccessScenario,
			want: probeCommandOutcome{
				stdout:   "Sent probe 1 (16 bytes) to <aabb>\nValid reply from <aabb>\nRound-trip time is 100.0 milliseconds over 2 hops\nSent 1, received 1, packet loss 0.00%\n",
				exitCode: 0,
			},
		},
		{
			name: "packet loss",
			got:  renderProbePacketLossScenario,
			want: probeCommandOutcome{
				stdout:   "Sent probe 1 (16 bytes) to <aabb>\nValid reply from <aabb>\nRound-trip time is 100.0 milliseconds over 2 hops\nSent probe 2 (16 bytes) to <aabb>\nProbe timed out\nSent 2, received 1, packet loss 50.00%\n",
				exitCode: 2,
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			compareProbeOutcomes(t, tc.got(), tc.want)
		})
	}
}

func buildGornprobeBinary(t *testing.T) string {
	t.Helper()
	gornprobeBuildOnce.Do(func() {
		binDir, err := os.MkdirTemp("", "gornprobe-int-bin-")
		if err != nil {
			gornprobeBuildErr = err
			return
		}
		gornprobeBinPath = filepath.Join(binDir, "gornprobe")
		cmd := osexec.Command("go", "build", "-o", gornprobeBinPath, ".")
		cmd.Dir = "."
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		gornprobeBuildErr = cmd.Run()
	})
	if gornprobeBuildErr != nil {
		t.Fatalf("failed to build gornprobe: %v", gornprobeBuildErr)
	}
	return gornprobeBinPath
}

func runGornprobeCommand(t *testing.T, args ...string) probeCommandOutcome {
	t.Helper()
	binPath := buildGornprobeBinary(t)
	cmd := osexec.Command(binPath, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*osexec.ExitError); ok {
			return probeCommandOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitErr.ExitCode()}
		}
		t.Fatalf("gornprobe failed: %v", err)
	}
	return probeCommandOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: 0}
}

func runRnprobeCommand(t *testing.T, args ...string) probeCommandOutcome {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", "original-reticulum-repo"))
	if err != nil {
		t.Fatalf("failed to resolve rnprobe repo root: %v", err)
	}
	scriptPath := filepath.Join("RNS", "Utilities", "rnprobe.py")
	cmd := osexec.Command("python3", append([]string{scriptPath}, args...)...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "PYTHONPATH="+repoRoot)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*osexec.ExitError); ok {
			return probeCommandOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitErr.ExitCode()}
		}
		t.Fatalf("rnprobe failed: %v", err)
	}
	return probeCommandOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: 0}
}

func compareProbeOutcomes(t *testing.T, got, want probeCommandOutcome) {
	t.Helper()
	if normalizeProbeOutput(got.stdout) != normalizeProbeOutput(want.stdout) {
		t.Fatalf("stdout mismatch\n--- got ---\n%v\n--- want ---\n%v", got.stdout, want.stdout)
	}
	if normalizeProbeOutput(got.stderr) != normalizeProbeOutput(want.stderr) {
		t.Fatalf("stderr mismatch\n--- got ---\n%v\n--- want ---\n%v", got.stderr, want.stderr)
	}
	if got.exitCode != want.exitCode {
		t.Fatalf("exit code = %v, want %v", got.exitCode, want.exitCode)
	}
}

func normalizeProbeOutput(text string) string {
	text = strings.NewReplacer(
		"\r", " ",
		"\b", "",
		"⢄", "",
		"⢂", "",
		"⢁", "",
		"⡁", "",
		"⡈", "",
		"⡐", "",
		"⡠", "",
		"rnprobe.py", "probe",
		"gornprobe", "probe",
		"rnprobe", "probe",
		"Go Reticulum Probe Utility", "Reticulum Probe Utility",
	).Replace(text)
	fields := strings.Fields(text)
	if len(fields) == 2 && fields[0] == "probe" {
		fields[1] = "VERSION"
	}
	return strings.Join(fields, " ")
}

func renderProbeTimeoutScenario() probeCommandOutcome {
	var out bytes.Buffer
	out.WriteString(formatProbeSentLine(1, 16, []byte{0xaa, 0xbb}, ""))
	receipt := &rns.PacketReceipt{Status: rns.ReceiptSent}
	clock := &probeIntegrationClock{now: time.Unix(0, 0)}
	_ = waitForProbeReceiptAt(&out, receipt, 0.1, clock.Now, clock.Sleep)
	summary, exitCode := formatProbeLossSummary(1, 0)
	out.WriteString(summary + "\n")
	return probeCommandOutcome{stdout: normalizeProbeOutput(out.String()), exitCode: exitCode}
}

func renderProbeSuccessScenario() probeCommandOutcome {
	var out bytes.Buffer
	out.WriteString(formatProbeSentLine(1, 16, []byte{0xaa, 0xbb}, ""))
	receipt := &rns.PacketReceipt{Status: rns.ReceiptSent}
	clock := &probeIntegrationClock{now: time.Unix(0, 0)}
	if waitForProbeReceiptAt(&out, receipt, 1.0, clock.Now, func(d time.Duration) {
		clock.Sleep(d)
		receipt.Status = rns.ReceiptDelivered
	}) {
		out.WriteString("\b\b \n")
		out.WriteString(formatProbeReplyLine([]byte{0xaa, 0xbb}, 0.1, 2, ""))
	}
	summary, exitCode := formatProbeLossSummary(1, 1)
	out.WriteString(summary + "\n")
	return probeCommandOutcome{stdout: normalizeProbeOutput(out.String()), exitCode: exitCode}
}

func renderProbePacketLossScenario() probeCommandOutcome {
	var out bytes.Buffer
	clock := &probeIntegrationClock{now: time.Unix(0, 0)}
	receipt := &rns.PacketReceipt{Status: rns.ReceiptSent}

	out.WriteString(formatProbeSentLine(1, 16, []byte{0xaa, 0xbb}, ""))
	if waitForProbeReceiptAt(&out, receipt, 1.0, clock.Now, func(d time.Duration) {
		clock.Sleep(d)
		receipt.Status = rns.ReceiptDelivered
	}) {
		out.WriteString("\b\b \n")
		out.WriteString(formatProbeReplyLine([]byte{0xaa, 0xbb}, 0.1, 2, ""))
	}

	out.WriteString(formatProbeSentLine(2, 16, []byte{0xaa, 0xbb}, ""))
	secondReceipt := &rns.PacketReceipt{Status: rns.ReceiptSent}
	if !waitForProbeReceiptAt(&out, secondReceipt, 0.1, clock.Now, clock.Sleep) {
		// timeout output already written by the helper
	}
	summary, exitCode := formatProbeLossSummary(2, 1)
	out.WriteString(summary + "\n")
	return probeCommandOutcome{stdout: normalizeProbeOutput(out.String()), exitCode: exitCode}
}

func tempDir(t *testing.T) string {
	t.Helper()
	baseDir := ""
	if runtime.GOOS == "darwin" {
		baseDir = "/tmp"
	}
	dir, err := os.MkdirTemp(baseDir, "gornprobe-int-")
	if err != nil {
		t.Fatalf("tempDir error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}
