// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
)

func TestParseFlags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		want     *appT
		wantErr  error
		wantText string
	}{
		{
			name: "short and long aliases",
			args: []string{"--config", "/tmp/config", "--probes", "5", "--size", "32", "--timeout", "10", "--wait", "1.5", "-vv", "full.name", "abcdef"},
			want: &appT{configDir: "/tmp/config", size: 32, probes: 5, timeout: 10, wait: 1.5, verbose: true, args: []string{"full.name", "abcdef"}},
		},
		{
			name:     "help flag",
			args:     []string{"--help"},
			wantErr:  errHelp,
			wantText: usageText,
		},
		{
			name: "version flag",
			args: []string{"--version"},
			want: &appT{size: DefaultProbeSize, probes: 1, version: true},
		},
		{
			name: "missing destination hash",
			args: []string{"full.name"},
			want: &appT{size: DefaultProbeSize, probes: 1, args: []string{"full.name"}},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out strings.Builder
			app, err := parseFlags(tc.args, &out)
			if tc.wantErr != nil {
				if err != tc.wantErr {
					t.Fatalf("parseFlags error = %v, want %v", err, tc.wantErr)
				}
				if tc.wantText != "" && out.String() != tc.wantText {
					t.Fatalf("usage text mismatch:\n--- got ---\n%v\n--- want ---\n%v", out.String(), tc.wantText)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFlags failed: %v", err)
			}
			if app.configDir != tc.want.configDir || app.size != tc.want.size || app.probes != tc.want.probes || app.timeout != tc.want.timeout || app.wait != tc.want.wait || app.verbose != tc.want.verbose || app.version != tc.want.version || len(app.args) != len(tc.want.args) {
				t.Fatalf("unexpected app state: %+v want %+v", app, tc.want)
			}
			for i := range tc.want.args {
				if app.args[i] != tc.want.args[i] {
					t.Fatalf("args[%v] = %q, want %q", i, app.args[i], tc.want.args[i])
				}
			}
		})
	}
}

func TestUsageText(t *testing.T) {
	t.Parallel()
	if got := strings.TrimSpace(usageText); !strings.Contains(got, "usage: gornprobe") || !strings.Contains(got, "Go Reticulum Probe Utility") || !strings.Contains(got, "--verbose") {
		t.Fatalf("usage text missing expected content: %v", got)
	}
}
