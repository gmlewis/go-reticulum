// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"io"
	"testing"
)

func TestParseFlags(t *testing.T) {
	t.Parallel()

	app, err := parseFlags([]string{"--config", "/tmp/config", "-t", "-m", "3", "-r", "-d", "-D", "-x", "-b", "-B", "-U", "-p", "-i", "identity.key", "-R", "0123456789abcdef0123456789abcdef", "-W", "44", "--duration", "12", "--reason", "test", "-w", "22", "-j", "-v", "dest"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if app.configDir != "/tmp/config" || !app.table || app.maxHops != 3 || !app.rates || !app.drop || !app.dropAnnounces || !app.dropVia || !app.blackholed || !app.blackhole || !app.unblackhole || !app.blackholedList || app.identityPath != "identity.key" || app.remoteHash != "0123456789abcdef0123456789abcdef" || app.remoteTimeout != 44 || app.duration != 12 || app.reason != "test" || app.timeout != 22 || !app.jsonOut || app.verbose != 1 || len(app.args) != 1 || app.args[0] != "dest" {
		t.Fatalf("unexpected app state: %+v", app)
	}
}

func TestParseFlagsLongAliases(t *testing.T) {
	t.Parallel()

	app, err := parseFlags([]string{"--config", "/tmp/config", "--table", "--max", "4", "--rates", "--drop", "--drop-announces", "--drop-via", "--blackholed", "--blackhole", "--unblackhole", "--blackholed-list", "--identity", "identity.key", "--remote", "0123456789abcdef0123456789abcdef", "--duration", "12", "--reason", "test", "--json", "--verbose", "dest"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if app.configDir != "/tmp/config" || !app.table || app.maxHops != 4 || !app.rates || !app.drop || !app.dropAnnounces || !app.dropVia || !app.blackholed || !app.blackhole || !app.unblackhole || !app.blackholedList || app.identityPath != "identity.key" || app.remoteHash != "0123456789abcdef0123456789abcdef" || app.duration != 12 || app.reason != "test" || !app.jsonOut || app.verbose != 1 || len(app.args) != 1 || app.args[0] != "dest" {
		t.Fatalf("unexpected app state from long aliases: %+v", app)
	}
}

func TestVerboseCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantVerbose int
	}{
		{"no flags", []string{}, 0},
		{"single -v", []string{"-v"}, 1},
		{"double -vv", []string{"-v", "-v"}, 2},
		{"triple -vvv", []string{"-v", "-v", "-v"}, 3},
		{"single --verbose", []string{"--verbose"}, 1},
		{"mixed", []string{"-v", "--verbose"}, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			app, err := parseFlags(tt.args, io.Discard)
			if err != nil {
				t.Fatalf("parseFlags failed: %v", err)
			}
			if int(app.verbose) != tt.wantVerbose {
				t.Errorf("verbose = %d, want %d", int(app.verbose), tt.wantVerbose)
			}
		})
	}
}
