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
	app, err := parseFlags([]string{"--config", "/tmp/config", "-v", "-q", "-s", "--exampleconfig", "--version"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if app.configDir != "/tmp/config" || !app.verbose || !app.quiet || !app.service || !app.exampleConfig || !app.version {
		t.Fatalf("unexpected app state: %+v", app)
	}
}
