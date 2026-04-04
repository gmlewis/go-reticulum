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
	var c counter
	for i := 0; i < 3; i++ {
		if err := c.Set("true"); err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	}
	if int(c) != 3 {
		t.Fatalf("counter = %v, want 3", int(c))
	}
}

func TestCounterIsBoolFlag(t *testing.T) {
	t.Parallel()
	var c counter
	if !c.IsBoolFlag() {
		t.Fatal("IsBoolFlag() = false, want true")
	}
}

func TestCounterString(t *testing.T) {
	t.Parallel()
	var c counter
	if c.String() != "0" {
		t.Fatalf("String() = %q, want %q", c.String(), "0")
	}
}

func TestAppFlags(t *testing.T) {
	t.Parallel()
	app := newApp()
	fs := flag.NewFlagSet("gornid", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	app.initFlags(fs)
	if err := fs.Parse([]string{"--verbose", "--quiet", "--version"}); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if app.verbose != 1 || app.quiet != 1 || !app.version {
		t.Fatalf("unexpected app state: %+v", app)
	}
}

func TestLongFormParserAliases(t *testing.T) {
	t.Parallel()
	app := newApp()
	fs := flag.NewFlagSet("gornid", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	app.initFlags(fs)
	if err := fs.Parse([]string{"--generate", "out.id", "--identity", "in.id", "--print-identity"}); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if app.generatePath != "out.id" || app.identityPath != "in.id" || !app.printIdentity {
		t.Fatalf("unexpected alias state: %+v", app)
	}
}
