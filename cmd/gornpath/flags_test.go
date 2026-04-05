// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"flag"
	"io"
	"testing"
)

func TestParseFlags(t *testing.T) {
	t.Parallel()
	flag.CommandLine = flag.NewFlagSet("gornpath", flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)

	app, err := parseFlags([]string{"--config", "/tmp/config", "-t", "-m", "3", "-r", "-d", "-D", "-x", "-b", "-B", "-U", "-p", "-i", "identity.key", "-R", "0123456789abcdef0123456789abcdef", "-W", "44", "--duration", "12", "--reason", "test", "-w", "22", "-j", "-v", "dest"})
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if app.configDir != "/tmp/config" || !app.table || app.maxHops != 3 || !app.rates || !app.drop || !app.dropAnnounces || !app.dropVia || !app.blackholed || !app.blackhole || !app.unblackhole || !app.blackholedList || app.identityPath != "identity.key" || app.remoteHash != "0123456789abcdef0123456789abcdef" || app.remoteTimeout != 44 || app.duration != 12 || app.reason != "test" || app.timeout != 22 || !app.jsonOut || !app.verbose || len(app.args) != 1 || app.args[0] != "dest" {
		t.Fatalf("unexpected app state: %+v", app)
	}
}
