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
	fs := flag.NewFlagSet("gornpkg", flag.ContinueOnError)
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
