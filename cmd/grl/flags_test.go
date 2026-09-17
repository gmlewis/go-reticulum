// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"flag"
	"strings"
	"testing"
)

// TestParseFlagsDefaults asserts a bare command line overrides nothing, so
// every choice is left to the configuration file.
func TestParseFlagsDefaults(t *testing.T) {
	t.Parallel()

	opts, err := parseFlagsTo(nil, &strings.Builder{})
	if err != nil {
		t.Fatalf("parseFlagsTo: %v", err)
	}
	for name, value := range map[string]string{
		"config":       opts.configPath,
		"portal-addr":  opts.portalAddr,
		"gps-port":     opts.gpsPort,
		"compass-port": opts.compassPort,
	} {
		if value != "" {
			t.Errorf("-%v defaulted to %q, want the empty string", name, value)
		}
	}
	if opts.quiet || opts.verbose || opts.version {
		t.Errorf("a bare command line set a boolean: %+v", opts)
	}
}

// TestParseFlagsEveryOption asserts every documented flag reaches its field.
func TestParseFlagsEveryOption(t *testing.T) {
	t.Parallel()

	opts, err := parseFlagsTo([]string{
		"-config", "/tmp/grl/config.toml",
		"-portal-addr", "0.0.0.0:9111",
		"-gps-port", "/dev/ttyUSB0",
		"-compass-port", "/dev/ttyUSB1",
		"-verbose",
	}, &strings.Builder{})
	if err != nil {
		t.Fatalf("parseFlagsTo: %v", err)
	}
	for name, got := range map[string]string{
		"-config":       opts.configPath,
		"-portal-addr":  opts.portalAddr,
		"-gps-port":     opts.gpsPort,
		"-compass-port": opts.compassPort,
	} {
		if got == "" {
			t.Errorf("%v did not reach its field", name)
		}
	}
	if !opts.verbose {
		t.Error("-verbose did not reach its field")
	}

	quiet, err := parseFlagsTo([]string{"-quiet"}, &strings.Builder{})
	if err != nil {
		t.Fatalf("parseFlagsTo: %v", err)
	}
	if !quiet.quiet {
		t.Error("-quiet did not reach its field")
	}
}

// TestParseFlagsHelp asserts -h and -help ask for the usage text, and that the
// text documents every flag and every file the tool uses.
func TestParseFlagsHelp(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"-h", "-help", "--help"} {
		var out strings.Builder
		_, err := parseFlagsTo([]string{arg}, &out)
		if !errors.Is(err, errHelp) {
			t.Fatalf("parseFlagsTo(%v) error = %v, want %v", arg, err, errHelp)
		}
		text := out.String()
		for _, want := range []string{
			"usage: grl",
			"--version", "--config", "--portal-addr", "--gps-port",
			"--compass-port", "--quiet", "--verbose",
			"GRL_HOME", "config.toml", "localhost:9111",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("the usage text from %v does not mention %q", arg, want)
			}
		}
	}
}

// TestParseFlagsVersion asserts -version and -V both ask for the version.
func TestParseFlagsVersion(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"-version", "-V"} {
		opts, err := parseFlagsTo([]string{arg}, &strings.Builder{})
		if err != nil {
			t.Fatalf("parseFlagsTo(%v): %v", arg, err)
		}
		if !opts.version {
			t.Errorf("%v did not ask for the version", arg)
		}
	}
}

// TestParseFlagsBadInput asserts a command line that cannot mean anything is
// refused rather than silently ignored.
func TestParseFlagsBadInput(t *testing.T) {
	t.Parallel()

	t.Run("unknown flag", func(t *testing.T) {
		t.Parallel()
		if _, err := parseFlagsTo([]string{"-bogus"}, &strings.Builder{}); err == nil {
			t.Fatal("an unknown flag was accepted")
		}
		if _, err := parseFlagsTo([]string{"-bogus"}, &strings.Builder{}); errors.Is(err, errHelp) {
			t.Error("an unknown flag was reported as a help request")
		}
	})

	t.Run("positional argument", func(t *testing.T) {
		t.Parallel()
		_, err := parseFlagsTo([]string{"serve", "now"}, &strings.Builder{})
		if err == nil {
			t.Fatal("a positional argument was accepted")
		}
		if !strings.Contains(err.Error(), "unrecognized arguments") {
			t.Errorf("error = %v, want it to name the unrecognized arguments", err)
		}
	})

	t.Run("two verbosity levels at once", func(t *testing.T) {
		t.Parallel()
		if _, err := parseFlagsTo([]string{"-quiet", "-verbose"}, &strings.Builder{}); err == nil {
			t.Fatal("-quiet -verbose was accepted, want a refusal")
		}
	})
}

// TestFlagsDoNotUseTheGlobalFlagSet asserts the tool never touches
// flag.CommandLine, which every other CLI in this repository also avoids.
func TestFlagsDoNotUseTheGlobalFlagSet(t *testing.T) {
	t.Parallel()

	before := flag.CommandLine.NFlag()
	if _, err := parseFlagsTo([]string{"-quiet"}, &strings.Builder{}); err != nil {
		t.Fatalf("parseFlagsTo: %v", err)
	}
	if flag.CommandLine.NFlag() != before {
		t.Error("parseFlagsTo wrote to the global flag set")
	}
}
