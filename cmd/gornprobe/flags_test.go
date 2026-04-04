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
	flag.CommandLine = flag.NewFlagSet("gornprobe", flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	app, err := parseFlags([]string{"--config", "/tmp/config", "-n", "5", "-s", "32", "-t", "10", "-w", "1.5", "-v", "full.name", "abcdef"})
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if app.configDir != "/tmp/config" || app.size != 32 || app.probes != 5 || app.timeout != 10 || app.wait != 1.5 || !app.verbose || len(app.args) != 2 {
		t.Fatalf("unexpected app state: %+v", app)
	}
}
