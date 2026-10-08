// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestGonsensorFlagDefaults pins the defaults the Android side and the docs are
// written against, because a changed default silently changes the emission rate
// of a stream nobody is watching.
func TestGonsensorFlagDefaults(t *testing.T) {
	t.Parallel()

	opts, err := parseFlags(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags(nil): %v", err)
	}
	if opts.rate != 1 {
		t.Fatalf("rate = %v, want 1", opts.rate)
	}
	if opts.compassRate != 10 {
		t.Fatalf("compassRate = %v, want 10", opts.compassRate)
	}
	if opts.statusInterval != 5 {
		t.Fatalf("statusInterval = %v, want 5", opts.statusInterval)
	}
	if opts.noRMC || opts.noGGA || opts.noHDM || opts.noHDT {
		t.Fatalf("a sentence switch defaulted to suppressing output: %+v", opts)
	}
	if opts.version {
		t.Fatalf("version defaulted to true")
	}
}

// TestGonsensorFlagsParsesEverySwitch asserts each documented flag is accepted
// and lands in the field it names, so the documented command line is the real
// one.
func TestGonsensorFlagsParsesEverySwitch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		args  []string
		check func(*testing.T, *options)
	}{
		{"rate", []string{"--rate", "2.5"}, func(t *testing.T, o *options) {
			if o.rate != 2.5 {
				t.Fatalf("rate = %v, want 2.5", o.rate)
			}
		}},
		{"compass rate", []string{"--compass-rate", "25"}, func(t *testing.T, o *options) {
			if o.compassRate != 25 {
				t.Fatalf("compassRate = %v, want 25", o.compassRate)
			}
		}},
		{"status interval", []string{"--status-interval", "30"}, func(t *testing.T, o *options) {
			if o.statusInterval != 30 {
				t.Fatalf("statusInterval = %v, want 30", o.statusInterval)
			}
		}},
		{"no rmc", []string{"--no-rmc"}, func(t *testing.T, o *options) { requireTrue(t, "--no-rmc", o.noRMC) }},
		{"no gga", []string{"--no-gga"}, func(t *testing.T, o *options) { requireTrue(t, "--no-gga", o.noGGA) }},
		{"no hdm", []string{"--no-hdm"}, func(t *testing.T, o *options) { requireTrue(t, "--no-hdm", o.noHDM) }},
		{"no hdt", []string{"--no-hdt"}, func(t *testing.T, o *options) { requireTrue(t, "--no-hdt", o.noHDT) }},
		{"version", []string{"--version"}, func(t *testing.T, o *options) { requireTrue(t, "--version", o.version) }},
		{"help", []string{"--help"}, func(t *testing.T, o *options) {
			t.Fatalf("--help returned options rather than the help sentinel: %+v", o)
		}},
		{"short help", []string{"-h"}, func(t *testing.T, o *options) {
			t.Fatalf("-h returned options rather than the help sentinel: %+v", o)
		}},
		{"rate zero disables limiting", []string{"--rate", "0"}, func(t *testing.T, o *options) {
			if o.rate != 0 {
				t.Fatalf("rate = %v, want 0", o.rate)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts, err := parseFlags(tc.args, io.Discard)
			if tc.name == "help" || tc.name == "short help" {
				if !errors.Is(err, errHelp) {
					t.Fatalf("parseFlags(%v) error = %v, want errHelp", tc.args, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFlags(%v): %v", tc.args, err)
			}
			tc.check(t, opts)
		})
	}
}

// TestGonsensorRejectsBadFlags asserts a malformed command line is refused
// loudly, because a silently ignored flag produces a stream nobody expects.
func TestGonsensorRejectsBadFlags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"--nonsense"}},
		{"rate is not a number", []string{"--rate", "fast"}},
		{"rate is negative", []string{"--rate", "-1"}},
		{"compass rate is negative", []string{"--compass-rate", "-0.5"}},
		{"status interval is negative", []string{"--status-interval", "-5"}},
		{"rate has no value", []string{"--rate"}},
		{"positional argument", []string{"unexpected"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := parseFlags(tc.args, io.Discard); err == nil {
				t.Fatalf("parseFlags(%v) accepted a malformed command line", tc.args)
			}
		})
	}
}

// TestGonsensorUsageText asserts the usage text documents every flag, so the
// help output cannot drift away from the parser.
func TestGonsensorUsageText(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{
		"--rate", "--compass-rate", "--no-rmc", "--no-gga", "--no-hdm",
		"--no-hdt", "--status-interval", "--version",
	} {
		if !strings.Contains(usageText, flag) {
			t.Fatalf("usageText does not mention %v", flag)
		}
	}
	if !strings.HasPrefix(usageText, "usage: gonsensor") {
		t.Fatalf("usageText = %q, want a gonsensor usage line", usageText[:min(40, len(usageText))])
	}
}

// TestGonsensorUsageIsWritable asserts asking for help prints the usage text to
// the writer the caller chose rather than to the process's standard error.
func TestGonsensorUsageIsWritable(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if _, err := parseFlags([]string{"--help"}, &buf); !errors.Is(err, errHelp) {
		t.Fatalf("parseFlags(--help) error = %v, want errHelp", err)
	}
	if !strings.Contains(buf.String(), "usage: gonsensor") {
		t.Fatalf("usage output = %q, want the usage text", buf.String())
	}
}

// requireTrue fails a subtest whose boolean flag did not take.
func requireTrue(t *testing.T, name string, got bool) {
	t.Helper()
	if !got {
		t.Fatalf("%v did not take effect", name)
	}
}
