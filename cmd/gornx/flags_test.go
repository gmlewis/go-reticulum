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
	app, err := parseFlags([]string{"--config", "/tmp/config", "-i", "/tmp/id", "-v", "-q", "-l", "-x", "dest", "command"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if app.configDir != "/tmp/config" || app.identityPath != "/tmp/id" || !app.verbose || !app.quiet || !app.listenMode || !app.interactive || len(app.args) != 2 {
		t.Fatalf("unexpected app state: %+v", app)
	}
}
