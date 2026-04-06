// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"flag"
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
	app := newApp()
	fs := flag.NewFlagSet("gornir", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	app.initFlags(fs)
	if err := fs.Parse([]string{"--verbose", "--quiet", "--exampleconfig"}); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if app.verbose != 1 {
		t.Fatalf("verbose = %v, want %v", app.verbose, 1)
	}
	if app.quiet != 1 {
		t.Fatalf("quiet = %v, want %v", app.quiet, 1)
	}
	if !app.exampleConfig {
		t.Fatal("exampleConfig = false, want true")
	}
}

func TestLongFormParserAliases(t *testing.T) {
	t.Parallel()
	app := newApp()
	fs := flag.NewFlagSet("gornir", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	app.initFlags(fs)
	if err := fs.Parse([]string{"--verbose", "--quiet", "--exampleconfig"}); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if app.verbose != 1 || app.quiet != 1 || !app.exampleConfig {
		t.Fatalf("unexpected alias state: %+v", app)
	}
}

func TestParseFlagsHelp(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	_, err := parseFlags([]string{"--help"}, &buf)
	if err != utils.ErrHelp {
		t.Fatalf("parseFlags error = %v, want %v", err, utils.ErrHelp)
	}
	if got := buf.String(); got != usageText {
		t.Fatalf("help output mismatch:\n--- got ---\n%v\n--- want ---\n%v", got, usageText)
	}
}

func TestParseFlagsVersion(t *testing.T) {
	t.Parallel()
	app, err := parseFlags([]string{"--version"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if !app.version {
		t.Fatal("version = false, want true")
	}
}
