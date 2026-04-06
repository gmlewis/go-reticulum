// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"io"
	"testing"
	"time"
)

func TestTimeoutFlag(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"1", time.Second, false},
		{"0.5", 500 * time.Millisecond, false},
		{"1.5", 1500 * time.Millisecond, false},
		{"invalid", 0, true},
	}

	for _, tc := range tests {
		var d time.Duration
		tf := (*timeoutFlag)(&d)
		err := tf.Set(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("Set(%q) error = %v, wantErr %v", tc.input, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && d != tc.expected {
			t.Errorf("Set(%q) got %v, want %v", tc.input, d, tc.expected)
		}
	}
}

func TestCountFlag(t *testing.T) {
	var c countFlag
	if c.String() != "0" {
		t.Errorf("expected 0, got %s", c.String())
	}

	if err := c.Set("true"); err != nil {
		t.Fatal(err)
	}
	if c.String() != "1" {
		t.Errorf("expected 1, got %s", c.String())
	}

	if err := c.Set("true"); err != nil {
		t.Fatal(err)
	}
	if c.String() != "2" {
		t.Errorf("expected 2, got %s", c.String())
	}
}

func TestParseFlags(t *testing.T) {
	t.Parallel()
	app, err := parseFlags([]string{"--config", "/tmp/config", "--rnsconfig", "/tmp/rns", "--status", "--verbose", "--quiet", "--timeout", "2"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if app.configDir != "/tmp/config" {
		t.Fatalf("configDir = %q, want %q", app.configDir, "/tmp/config")
	}
	if app.rnsConfigDir != "/tmp/rns" {
		t.Fatalf("rnsConfigDir = %q, want %q", app.rnsConfigDir, "/tmp/rns")
	}
	if !app.displayStatus {
		t.Fatal("displayStatus = false, want true")
	}
	if app.verbosity != 1 || app.quietness != 1 {
		t.Fatalf("verbosity=%v quietness=%v, want 1 and 1", app.verbosity, app.quietness)
	}
	if app.timeout != 2*time.Second {
		t.Fatalf("timeout = %v, want 2s", app.timeout)
	}
}

func TestUsageText(t *testing.T) {
	t.Parallel()
	if got := bytes.NewBufferString(usageText).String(); got == "" || !bytes.Contains([]byte(got), []byte("Go Lightweight Extensible Messaging Daemon")) {
		t.Fatalf("usageText missing expected content: %q", got)
	}
}
