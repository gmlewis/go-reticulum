// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// newReticulumWithReticulumSection constructs a Reticulum over a temp config
// directory whose [reticulum] section is the supplied body, and returns the
// transport it configured.
func newReticulumWithReticulumSection(t *testing.T, body string) *TransportSystem {
	t.Helper()
	configDir := testutils.TempDir(t, tempDirPrefix)
	config := "[reticulum]\nshare_instance = No\n" + body + "\n[logging]\nloglevel = 4\n\n[interfaces]\n"
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	t.Cleanup(func() { closeReticulum(t, r) })
	return ts
}

// TestInboundQueueLengthsFromConfig pins Python's qlen_in_* parsing
// (Reticulum.py:707-721): each key overrides its class only when the value is
// a positive integer; absent, zero, negative and non-numeric values leave the
// class default.
func TestInboundQueueLengthsFromConfig(t *testing.T) {
	t.Parallel()

	defaults := [NumTrafficClasses]int{
		DefaultInboundDataQueueLength,
		DefaultInboundAnnounceQueueLength,
		DefaultInboundPRQueueLength,
		DefaultInboundILQueueLength,
	}

	tests := []struct {
		name string
		body string
		want [NumTrafficClasses]int
	}{
		{
			name: "absent keys keep the defaults",
			body: "",
			want: defaults,
		},
		{
			name: "all four override",
			body: "qlen_in_data = 10\nqlen_in_announce = 20\nqlen_in_pr = 30\nqlen_in_il = 40",
			want: [NumTrafficClasses]int{10, 20, 30, 40},
		},
		{
			name: "zero and negative values are ignored",
			body: "qlen_in_data = 0\nqlen_in_announce = -5\nqlen_in_pr = 30\nqlen_in_il = 40",
			want: [NumTrafficClasses]int{defaults[TCData], defaults[TCAnnounce], 30, 40},
		},
		{
			name: "non-numeric values are ignored",
			body: "qlen_in_data = lots\nqlen_in_announce = 20",
			want: [NumTrafficClasses]int{defaults[TCData], 20, defaults[TCPathRequest], defaults[TCIngressLimited]},
		},
		{
			name: "surrounding whitespace is tolerated",
			body: "qlen_in_data =   64  ",
			want: [NumTrafficClasses]int{64, defaults[TCAnnounce], defaults[TCPathRequest], defaults[TCIngressLimited]},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := newReticulumWithReticulumSection(t, tt.body)
			if got := ts.InboundQueueLengths(); got != tt.want {
				t.Errorf("InboundQueueLengths() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestInboundQueueDefaultsMatchPython pins the four defaults against the
// installed 1.5.0 reference, the release that introduced the queues. The
// repo-standard python3 is RNS 1.4.2, which has no InboundQueues at all, so
// the probe explicitly looks for 1.5.0.
func TestInboundQueueDefaultsMatchPython(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	const script = `
import json, sys
import RNS
json.dump({
    "data": RNS.Transport.INBOUND_DA_QUEUE_LENGTH,
    "announce": RNS.Transport.INBOUND_AN_QUEUE_LENGTH,
    "pr": RNS.Transport.INBOUND_PR_QUEUE_LENGTH,
    "il": RNS.Transport.INBOUND_IL_QUEUE_LENGTH,
}, sys.stdout)
`
	var want struct {
		Data     int `json:"data"`
		Announce int `json:"announce"`
		PR       int `json:"pr"`
		IL       int `json:"il"`
	}
	if err := json.Unmarshal([]byte(runPythonRNS15(t, script, "")), &want); err != nil {
		t.Fatalf("decode python output: %v", err)
	}
	got := NewTransportSystem(nil).InboundQueueLengths()
	wantLengths := [NumTrafficClasses]int{want.Data, want.Announce, want.PR, want.IL}
	if got != wantLengths {
		t.Errorf("default inbound queue lengths = %v, want %v (Python Transport.INBOUND_*_QUEUE_LENGTH)", got, wantLengths)
	}
}
