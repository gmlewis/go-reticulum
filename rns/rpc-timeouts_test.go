// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// runPythonRNS15 runs script with an interpreter whose RNS is 1.5.0, the
// release that introduced Transport.medium_path_timeout. The test skips only
// when no 1.5.0 interpreter is present, so the parity check runs everywhere the
// symbol exists.
func runPythonRNS15(t *testing.T, script, arg string) string {
	t.Helper()
	for _, exe := range []string{"/opt/homebrew/bin/python3", "python3", "python3.14"} {
		out, err := exec.Command(exe, "-c", "import RNS; print(RNS.__version__)").Output()
		if err != nil || !strings.HasPrefix(string(out), "1.5.0") {
			continue
		}
		cmd := exec.Command(exe, "-c", script, arg)
		cmd.Stderr = os.Stderr
		stdout, err := cmd.Output()
		if err != nil {
			t.Fatalf("%v with RNS 1.5.0 failed: %v", exe, err)
		}
		return string(stdout)
	}
	t.Skip("no RNS 1.5.0 interpreter available; Transport.medium_path_timeout is a 1.5.0 API")
	return ""
}

// TestMediumPathTimeoutPythonParity derives Python Transport.medium_path_timeout
// (Transport.py:3133-3136) for a bitrate battery and compares the Go
// computation, which reads the same value from the transport's live interfaces.
func TestMediumPathTimeoutPythonParity(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	bitrates := []int{0, 1, 2, 5, 10, 1000, 62500, 1000000}
	in, err := json.Marshal(bitrates)
	if err != nil {
		t.Fatal(err)
	}

	const script = `
import sys, json
import RNS
out = []
for b in json.loads(sys.argv[1]):
    RNS.Transport.lowest_interface_bitrate = b
    out.append(RNS.Transport.medium_path_timeout())
json.dump(out, sys.stdout)
`
	var want []float64
	if err := json.Unmarshal([]byte(runPythonRNS15(t, script, string(in))), &want); err != nil {
		t.Fatalf("decode python output: %v", err)
	}

	for i, bitrate := range bitrates {
		ts := NewTransportSystem(nil)
		if bitrate > 0 {
			iface := newAFI("test", 0)
			iface.bitrate = bitrate
			ts.interfaces = []interfaces.Interface{iface}
		}
		if got := ts.MediumPathTimeout(); got != want[i] {
			t.Errorf("MediumPathTimeout(bitrate=%v) = %v, want %v (Python)", bitrate, got, want[i])
		}
	}
}

// TestLowestInterfaceBitrate pins the selection rule: online interfaces only,
// and only those reporting a positive bitrate (Transport.py:557-558).
func TestLowestInterfaceBitrate(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	online := newAFI("online", 0)
	online.bitrate = 1000
	fast := newAFI("fast", 0)
	fast.bitrate = 100000
	offline := newAFI("offline", 0)
	offline.bitrate = 10
	offline.status = false
	unknown := newAFI("unknown", 0)
	unknown.bitrate = 0

	ts.interfaces = []interfaces.Interface{online, fast, offline, unknown}
	if got := ts.LowestInterfaceBitrate(); got != 1000 {
		t.Errorf("LowestInterfaceBitrate = %v, want 1000 (offline and zero-bitrate interfaces excluded)", got)
	}
}

// TestActiveLinkCount pins Python Transport.active_link_count
// (Transport.py:3142-3144): validated link-table entries only.
func TestActiveLinkCount(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	ts.linkTable = map[string]*LinkEntry{
		"validated": {Validated: true},
		"pending":   {Validated: false},
		"other":     {Validated: true},
	}
	if got := ts.ActiveLinkCount(); got != 2 {
		t.Errorf("ActiveLinkCount = %v, want 2", got)
	}
	r := &Reticulum{transport: ts}
	if got := r.getActiveLinkCount(); got != 2 {
		t.Errorf("getActiveLinkCount = %v, want 2", got)
	}
}
