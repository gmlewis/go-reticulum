// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"io"
	"testing"

	"github.com/gmlewis/go-reticulum/utils"
)

func TestCounter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		calls int
		want  int
	}{
		{"zero", 0, 0},
		{"one", 1, 1},
		{"three", 3, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var c counter
			for i := 0; i < tc.calls; i++ {
				if err := c.Set("true"); err != nil {
					t.Fatalf("Set failed: %v", err)
				}
			}
			if int(c) != tc.want {
				t.Errorf("counter = %v, want %v", int(c), tc.want)
			}
		})
	}
}

func TestCounterIsBoolFlag(t *testing.T) {
	t.Parallel()
	var c counter
	if !c.IsBoolFlag() {
		t.Error("IsBoolFlag() = false, want true")
	}
}

func TestCounterString(t *testing.T) {
	t.Parallel()
	var c counter
	if c.String() != "0" {
		t.Errorf("String() = %q, want %q", c.String(), "0")
	}
	c = 5
	if c.String() != "5" {
		t.Errorf("String() = %q, want %q", c.String(), "5")
	}
}

func TestAppFlags(t *testing.T) {
	t.Parallel()
	app, args, err := parseFlags([]string{"--config", "/tmp/config", "--all", "--json", "--verbose", "--monitor-interval", "2", "filter"}, io.Discard)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(args) != 1 || args[0] != "filter" {
		t.Fatalf("args = %v, want [filter]", args)
	}
	if app.configDir != "/tmp/config" {
		t.Fatalf("configDir = %q, want %q", app.configDir, "/tmp/config")
	}
	if !app.showAll {
		t.Fatal("showAll = false, want true")
	}
	if !app.jsonOutput {
		t.Fatal("jsonOutput = false, want true")
	}
	if app.verbose != 1 {
		t.Fatalf("verbose = %v, want %v", app.verbose, 1)
	}
	if app.monitorInterval != 2 {
		t.Fatalf("monitorInterval = %v, want 2", app.monitorInterval)
	}
}

func TestParseFlagsHelp(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	app, args, err := parseFlags([]string{"--help"}, &buf)
	if err != utils.ErrHelp {
		t.Fatalf("parseFlags error = %v, want %v", err, utils.ErrHelp)
	}
	if app == nil {
		t.Fatal("parseFlags returned nil app")
	}
	if len(args) != 0 {
		t.Fatalf("args = %v, want empty", args)
	}
	if buf.Len() == 0 {
		t.Fatal("help output was empty")
	}
	for _, want := range []string{"Reticulum Network Stack Status", "-a, --all", "-v, --verbose"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Fatalf("help output missing %q: %v", want, buf.String())
		}
	}
}
