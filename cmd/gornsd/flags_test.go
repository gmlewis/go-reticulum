// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

const pythonHelpText = `usage: rnsd.py [-h] [--config CONFIG] [-v] [-q] [-s] [-i] [--exampleconfig]
							 [--version]

Reticulum Network Stack Daemon

options:
	-h, --help         show this help message and exit
	--config CONFIG    path to alternative Reticulum config directory
	-v, --verbose
	-q, --quiet
	-s, --service      rnsd is running as a service and should log to file
	-i, --interactive  drop into interactive shell after initialisation
	--exampleconfig    print verbose configuration example to stdout and exit
	--version          show program's version number and exit
`

func normalizeHelpText(text string) string {
	text = strings.ReplaceAll(text, "gornsd", "rnsd")
	text = strings.ReplaceAll(text, "rnsd.py", "rnsd")
	text = strings.ReplaceAll(text, "Go Reticulum", "Reticulum")
	return strings.Join(strings.Fields(text), " ")
}

func TestCountFlagAccumulates(t *testing.T) {
	t.Parallel()

	var verbose int
	count := countFlag{target: &verbose}
	if got := count.String(); got != "0" {
		t.Fatalf("count.String() = %q, want 0", got)
	}
	if !count.IsBoolFlag() {
		t.Fatal("count.IsBoolFlag() = false, want true")
	}
	if err := count.Set("true"); err != nil {
		t.Fatalf("count.Set failed: %v", err)
	}
	if err := count.Set("true"); err != nil {
		t.Fatalf("count.Set failed: %v", err)
	}
	if got, want := verbose, 2; got != want {
		t.Fatalf("verbose = %v, want %v", got, want)
	}
	if err := count.Set("true"); err != nil {
		t.Fatalf("count.Set failed: %v", err)
	}
	if got, want := verbose, 3; got != want {
		t.Fatalf("verbose = %v, want %v", got, want)
	}
}

func TestParseFlagsSupportsAliasesAndCounts(t *testing.T) {
	t.Parallel()
	app, err := parseFlags([]string{"--config", "/tmp/config", "-v", "-v", "--verbose", "-q", "--quiet", "-s", "--service", "-i", "--interactive", "--exampleconfig", "--version"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if app.configDir != "/tmp/config" || app.verbose != 3 || app.quiet != 2 || !app.service || !app.interactive || !app.exampleConfig || !app.version {
		t.Fatalf("unexpected app state: %+v", app)
	}
}

func TestParseFlagsHelp(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	_, err := parseFlags([]string{"--help"}, &buf)
	if err != errHelp {
		t.Fatalf("parseFlags error = %v, want %v", err, errHelp)
	}
	if got := buf.String(); got != usageText {
		t.Fatalf("help output mismatch:\n--- got ---\n%v\n--- want ---\n%v", got, usageText)
	}
}

func TestUsageTextNormalizedParity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want string
	}{
		{name: "full help text", want: pythonHelpText},
		{name: "service description", want: "-s, --service rnsd is running as a service and should log to file"},
		{name: "interactive description", want: "-i, --interactive drop into interactive shell after initialisation"},
		{name: "exampleconfig description", want: "--exampleconfig print verbose configuration example to stdout and exit"},
		{name: "version description", want: "--version show program's version number and exit"},
	}
	got := normalizeHelpText(usageText)
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if test.name == "full help text" {
				if got != normalizeHelpText(test.want) {
					t.Fatalf("normalized usage text mismatch:\n--- got ---\n%v\n--- want ---\n%v", got, normalizeHelpText(test.want))
				}
				return
			}
			if !strings.Contains(got, normalizeHelpText(test.want)) {
				t.Fatalf("normalized usage text missing %q in %q", normalizeHelpText(test.want), got)
			}
		})
	}
}

func TestParseFlagsRejectsUnknownFlags(t *testing.T) {
	t.Parallel()
	_, err := parseFlags([]string{"--bogus"}, io.Discard)
	if err == nil {
		t.Fatal("parseFlags error = nil, want non-nil")
	}
	if got := err.Error(); !strings.Contains(got, "flag provided but not defined: -bogus") {
		t.Fatalf("error = %q, want flag parser failure", got)
	}
}

func TestParseFlagsRejectsPositionalArgs(t *testing.T) {
	t.Parallel()
	_, err := parseFlags([]string{"dest"}, io.Discard)
	if err == nil {
		t.Fatal("parseFlags error = nil, want non-nil")
	}
	if got := err.Error(); !strings.Contains(got, "unrecognized arguments: dest") {
		t.Fatalf("error = %q, want unrecognized-arguments failure", got)
	}
}

func TestParseFlagsRejectsArgsAfterDoubleDash(t *testing.T) {
	t.Parallel()
	_, err := parseFlags([]string{"--", "dest"}, io.Discard)
	if err == nil {
		t.Fatal("parseFlags error = nil, want non-nil")
	}
	if got := err.Error(); !strings.Contains(got, "unrecognized arguments: dest") {
		t.Fatalf("error = %q, want unrecognized-arguments failure", got)
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
