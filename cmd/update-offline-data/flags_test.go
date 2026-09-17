// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestParseFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantDryRun  bool
		wantVerbose bool
		wantTarget  string
		wantHelp    bool
		wantErr     bool
	}{
		{
			name:        "default",
			args:        []string{},
			wantDryRun:  false,
			wantVerbose: false,
			wantTarget:  "all",
		},
		{
			name:        "dry-run short",
			args:        []string{"-n"},
			wantDryRun:  true,
			wantVerbose: false,
			wantTarget:  "all",
		},
		{
			name:        "dry-run long",
			args:        []string{"--dry-run", "-v", "--target", "buoy"},
			wantDryRun:  true,
			wantVerbose: true,
			wantTarget:  "buoy",
		},
		{
			name:     "help flag",
			args:     []string{"-h"},
			wantHelp: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			opts, err := parseFlags(tt.args, &buf)
			if tt.wantHelp {
				if !errors.Is(err, errHelp) {
					t.Fatalf("expected errHelp, got %v", err)
				}
				if !strings.Contains(buf.String(), "usage: update-offline-data") {
					t.Fatalf("help text missing usage header: %v", buf.String())
				}
				return
			}
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if opts.dryRun != tt.wantDryRun {
				t.Errorf("dryRun = %v, want %v", opts.dryRun, tt.wantDryRun)
			}
			if opts.verbose != tt.wantVerbose {
				t.Errorf("verbose = %v, want %v", opts.verbose, tt.wantVerbose)
			}
			if opts.target != tt.wantTarget {
				t.Errorf("target = %v, want %v", opts.target, tt.wantTarget)
			}
		})
	}
}
