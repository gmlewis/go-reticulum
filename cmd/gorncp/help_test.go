// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
)

// TestHelpOutput verifies that the help output matches Python's argparse format.
func TestHelpOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		check func(output string) bool
	}{
		{
			name: "usage line",
			check: func(output string) bool {
				return strings.Contains(output, "usage:") &&
					strings.Contains(output, "[file]") &&
					strings.Contains(output, "[destination]")
			},
		},
		{
			name: "positional arguments section",
			check: func(output string) bool {
				return strings.Contains(output, "positional arguments:") &&
					strings.Contains(output, "file") &&
					strings.Contains(output, "destination")
			},
		},
		{
			name: "options section",
			check: func(output string) bool {
				return strings.Contains(output, "options:")
			},
		},
		{
			name: "help option",
			check: func(output string) bool {
				return strings.Contains(output, "-h, --help")
			},
		},
		{
			name: "config option",
			check: func(output string) bool {
				return strings.Contains(output, "--config path")
			},
		},
		{
			name: "verbose option",
			check: func(output string) bool {
				return strings.Contains(output, "-v, --verbose")
			},
		},
		{
			name: "quiet option",
			check: func(output string) bool {
				return strings.Contains(output, "-q, --quiet")
			},
		},
		{
			name: "silent option",
			check: func(output string) bool {
				return strings.Contains(output, "-S, --silent")
			},
		},
		{
			name: "listen option",
			check: func(output string) bool {
				return strings.Contains(output, "-l, --listen")
			},
		},
		{
			name: "no-compress option",
			check: func(output string) bool {
				return strings.Contains(output, "-C, --no-compress")
			},
		},
		{
			name: "allow-fetch option",
			check: func(output string) bool {
				return strings.Contains(output, "-F, --allow-fetch")
			},
		},
		{
			name: "fetch option",
			check: func(output string) bool {
				return strings.Contains(output, "-f, --fetch")
			},
		},
		{
			name: "jail option",
			check: func(output string) bool {
				return strings.Contains(output, "-j path, --jail path")
			},
		},
		{
			name: "save option",
			check: func(output string) bool {
				return strings.Contains(output, "-s path, --save path")
			},
		},
		{
			name: "overwrite option",
			check: func(output string) bool {
				return strings.Contains(output, "-O, --overwrite")
			},
		},
		{
			name: "announce option",
			check: func(output string) bool {
				return strings.Contains(output, "-b seconds")
			},
		},
		{
			name: "allowed option",
			check: func(output string) bool {
				return strings.Contains(output, "-a allowed_hash")
			},
		},
		{
			name: "no-auth option",
			check: func(output string) bool {
				return strings.Contains(output, "-n, --no-auth")
			},
		},
		{
			name: "print-identity option",
			check: func(output string) bool {
				return strings.Contains(output, "-p, --print-identity")
			},
		},
		{
			name: "identity option",
			check: func(output string) bool {
				return strings.Contains(output, "-i identity")
			},
		},
		{
			name: "timeout option",
			check: func(output string) bool {
				return strings.Contains(output, "-w seconds")
			},
		},
		{
			name: "phy-rates option",
			check: func(output string) bool {
				return strings.Contains(output, "-P, --phy-rates")
			},
		},
		{
			name: "version option",
			check: func(output string) bool {
				return strings.Contains(output, "--version")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := usageText
			if !tt.check(output) {
				t.Errorf("Help output check failed for %s\ngot: %v", tt.name, output)
			}
		})
	}
}
