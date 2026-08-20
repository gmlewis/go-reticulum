// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

func TestProbeMTUOverflowMessage(t *testing.T) {
	t.Parallel()

	if got, want := formatProbeMTUError(513), "Error: Probe packet size of 513 bytes exceed MTU of 500 bytes"; got != want {
		t.Fatalf("formatProbeMTUError() = %q, want %q", got, want)
	}
}

func TestProbeSentLine(t *testing.T) {
	t.Parallel()

	if got, want := formatProbeSentLine(1, 16, []byte{0xaa, 0xbb}, ""), "\rSent probe 1 (16 bytes) to "+rns.PrettyHex([]byte{0xaa, 0xbb})+"  "; got != want {
		t.Fatalf("formatProbeSentLine() = %q, want %q", got, want)
	}
}

func TestProbeVerboseMore(t *testing.T) {
	t.Parallel()

	if got, want := formatProbeVerboseMore(nil, "None"), ""; got != want {
		t.Fatalf("formatProbeVerboseMore(nil, \"None\") = %q, want %q", got, want)
	}
	if got, want := formatProbeVerboseMore([]byte{0x11, 0x22}, "eth0"), " via "+rns.PrettyHex([]byte{0x11, 0x22})+" on eth0"; got != want {
		t.Fatalf("formatProbeVerboseMore() = %q, want %q", got, want)
	}
}

func TestProbeReceptionStats(t *testing.T) {
	t.Parallel()

	rssi := -73.5
	snr := 9.25
	q := 0.87
	if got, want := formatProbeReceptionStats(&rssi, &snr, &q), " [RSSI -73.5 dBm] [SNR 9.25 dB] [Link Quality 0.87%]"; got != want {
		t.Fatalf("formatProbeReceptionStats() = %q, want %q", got, want)
	}
	if got, want := formatProbeReceptionStats(nil, nil, nil), ""; got != want {
		t.Fatalf("formatProbeReceptionStats(nil) = %q, want %q", got, want)
	}

	intRSSI := -73.0
	intSNR := 9.0
	intQ := 87.0
	if got, want := formatProbeReceptionStats(&intRSSI, &intSNR, &intQ), " [RSSI -73.0 dBm] [SNR 9.0 dB] [Link Quality 87.0%]"; got != want {
		t.Fatalf("formatProbeReceptionStats() = %q, want %q", got, want)
	}

	onlyRSSI := -50.0
	if got, want := formatProbeReceptionStats(&onlyRSSI, nil, nil), " [RSSI -50.0 dBm]"; got != want {
		t.Fatalf("formatProbeReceptionStats() = %q, want %q", got, want)
	}
}

func TestProbeRTTString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   float64
		want string
	}{
		{name: "seconds", in: 1.23456, want: "1.235 seconds"},
		{name: "milliseconds", in: 0.1, want: "100.0 milliseconds"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatProbeRTTString(tc.in); got != tc.want {
				t.Fatalf("formatProbeRTTString(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestProbeHopSuffix(t *testing.T) {
	t.Parallel()

	if got, want := formatProbeHopSuffix(1), ""; got != want {
		t.Fatalf("formatProbeHopSuffix(1) = %q, want %q", got, want)
	}
	if got, want := formatProbeHopSuffix(2), "s"; got != want {
		t.Fatalf("formatProbeHopSuffix(2) = %q, want %q", got, want)
	}
}

func TestProbeLossSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sent     int
		received int
		wantText string
		wantExit int
	}{
		{name: "no loss", sent: 4, received: 4, wantText: "Sent 4, received 4, packet loss 0.0%", wantExit: 0},
		{name: "partial loss", sent: 10, received: 7, wantText: "Sent 10, received 7, packet loss 30.0%", wantExit: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotText, gotExit := formatProbeLossSummary(tc.sent, tc.received)
			if gotText != tc.wantText {
				t.Fatalf("formatProbeLossSummary text = %q, want %q", gotText, tc.wantText)
			}
			if gotExit != tc.wantExit {
				t.Fatalf("formatProbeLossSummary exit = %v, want %v", gotExit, tc.wantExit)
			}
		})
	}
}

// TestProbeLossSummaryPythonParity is a LIVE cross-impl test: it execs python3 to
// compute rnprobe.py's exact loss-summary line (`f"Sent {s}, received {r}, packet
// loss {round((1-(r/s))*100,2)}%"`, rnprobe.py:206) for a range of (sent, received)
// pairs and diffs them against Go's formatProbeLossSummary. Python's str(round(x,2))
// drops trailing zeros ("100.0%", "0.0%", "50.0%", "25.0%", "10.0%") whereas a naive
// %.2f would emit "100.00%" etc. — this test catches that divergence.
func TestProbeLossSummaryPythonParity(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	pairs := [][2]int{{1, 0}, {1, 1}, {2, 1}, {3, 1}, {4, 3}, {10, 9}, {4, 4}, {10, 7}, {3, 2}, {7, 3}, {100, 97}}

	pyScript := `
import sys, json
pairs = json.loads(sys.argv[1])
out = {}
for s, r in pairs:
    loss = round((1-(r/s))*100, 2)
    out["%d,%d" % (s, r)] = "Sent %d, received %d, packet loss %s%%" % (s, r, loss)
print(json.dumps(out))
`
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "lossparity.py")
	if err := os.WriteFile(scriptPath, []byte(pyScript), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	pairsJSON, _ := json.Marshal(pairs)
	out, err := exec.Command("python3", scriptPath, string(pairsJSON)).Output()
	if err != nil {
		t.Fatalf("python3 loss script failed: %v\n%s", err, out)
	}
	var pyWant map[string]string
	if err := json.Unmarshal(out, &pyWant); err != nil {
		t.Fatalf("json unmarshal: %v\nraw: %s", err, out)
	}

	for _, p := range pairs {
		key := fmt.Sprintf("%d,%d", p[0], p[1])
		gotText, _ := formatProbeLossSummary(p[0], p[1])
		want := pyWant[key]
		if gotText != want {
			t.Errorf("formatProbeLossSummary(%d,%d) = %q, want Python %q", p[0], p[1], gotText, want)
		}
	}
}
