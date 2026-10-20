// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/json"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestFormatterPythonParity derives every expected value from the installed
// RNS reference on each run: prettydistance (RNS/__init__.py:226-239),
// prettyshorttime (RNS/__init__.py:294-338), host_os
// (RNS/vendor/platformutils.py:24-31) and phyparams (RNS/__init__.py:340-347).
func TestFormatterPythonParity(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	distances := []float64{0, 0.000001, 0.00001, 0.0005, 1, 12.5, 1000, 1234567, -5, -0.5}
	shorts := []struct {
		Time    float64 `json:"time"`
		Verbose bool    `json:"verbose"`
		Compact bool    `json:"compact"`
	}{
		{0, false, false},
		{0, true, true},
		{1.5, false, false},
		{1.5, true, false},
		{1.5, false, true},
		{0.001, false, false},
		{0.0015, false, false},
		{0.0000015, false, false},
		{-2.25, false, false},
		{3661.123456, false, false},
		{2, true, false},
	}
	payload, err := json.Marshal(map[string]any{
		"distances": distances,
		"shorts":    shorts,
	})
	if err != nil {
		t.Fatal(err)
	}

	const script = `
import sys, json, io, contextlib
import RNS
req = json.loads(sys.argv[1])
out = {
    "distances": [RNS.prettydistance(d) for d in req["distances"]],
    "shorts": [RNS.prettyshorttime(s["time"], verbose=s["verbose"], compact=s["compact"]) for s in req["shorts"]],
    "host_os": RNS.host_os(),
}
buf = io.StringIO()
with contextlib.redirect_stdout(buf):
    RNS.phyparams()
out["phy"] = buf.getvalue().rstrip("\n")
from RNS.vendor import platformutils as p
out["platform"] = {
    "android": p.is_android(), "linux": p.is_linux(), "darwin": p.is_darwin(),
    "windows": p.is_windows(), "epoll": p.use_epoll(), "af_unix": p.use_af_unix(),
}
json.dump(out, sys.stdout)
`
	var want struct {
		Distances []string `json:"distances"`
		Shorts    []string `json:"shorts"`
		HostOS    string   `json:"host_os"`
		Phy       string   `json:"phy"`
		Platform  struct {
			Android bool `json:"android"`
			Linux   bool `json:"linux"`
			Darwin  bool `json:"darwin"`
			Windows bool `json:"windows"`
			Epoll   bool `json:"epoll"`
			AFUnix  bool `json:"af_unix"`
		} `json:"platform"`
	}
	if err := json.Unmarshal([]byte(testutils.RunPython(t, script, string(payload))), &want); err != nil {
		t.Fatalf("decode python output: %v", err)
	}

	for i, d := range distances {
		if got := PrettyDistance(d, "m"); got != want.Distances[i] {
			t.Errorf("PrettyDistance(%v) = %q, want %q (Python)", d, got, want.Distances[i])
		}
	}
	for i, s := range shorts {
		if got := PrettyShortTime(s.Time, s.Verbose, s.Compact); got != want.Shorts[i] {
			t.Errorf("PrettyShortTime(%v, verbose=%v, compact=%v) = %q, want %q (Python)",
				s.Time, s.Verbose, s.Compact, got, want.Shorts[i])
		}
	}
	if got := HostOS(); got != want.HostOS {
		t.Errorf("HostOS() = %q, want %q (Python get_platform)", got, want.HostOS)
	}
	if got := PhyParams(); got != want.Phy {
		t.Errorf("PhyParams() =\n%v\nwant\n%v", got, want.Phy)
	}
	platformChecks := []struct {
		name string
		got  bool
		want bool
	}{
		{"is_android", IsAndroid(), want.Platform.Android},
		{"is_linux", IsLinux(), want.Platform.Linux},
		{"is_darwin", IsDarwin(), want.Platform.Darwin},
		{"is_windows", IsWindows(), want.Platform.Windows},
		{"use_epoll", UseEpoll(), want.Platform.Epoll},
		{"use_af_unix", UseAFUnix(), want.Platform.AFUnix},
	}
	for _, c := range platformChecks {
		if c.got != c.want {
			t.Errorf("%v() = %v, want %v (Python platformutils)", c.name, c.got, c.want)
		}
	}
}
