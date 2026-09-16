// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestParseFlagsDefaults asserts the command line defaults: the hard-coded
// official gonomadnet Public RRC Hub, the official gobot nick, the room the bot
// is in, and the timing.
func TestParseFlagsDefaults(t *testing.T) {
	t.Parallel()

	opts, err := parseFlags([]string{"help"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if opts.dest != DefaultDestination {
		t.Errorf("dest = %q, want %q", opts.dest, DefaultDestination)
	}
	if opts.target != DefaultTargetNick {
		t.Errorf("target = %q, want %q", opts.target, DefaultTargetNick)
	}
	if opts.room != DefaultRoom {
		t.Errorf("room = %q, want %q", opts.room, DefaultRoom)
	}
	if opts.nick != DefaultOwnNick {
		t.Errorf("nick = %q, want %q", opts.nick, DefaultOwnNick)
	}
	if opts.timeout != DefaultTimeout {
		t.Errorf("timeout = %v, want %v", opts.timeout, DefaultTimeout)
	}
	if opts.quiet != DefaultQuietPeriod {
		t.Errorf("quiet = %v, want %v", opts.quiet, DefaultQuietPeriod)
	}
	if opts.message != "help" {
		t.Errorf("message = %q, want %q", opts.message, "help")
	}
}

// TestParseFlagsJoinsPositionalArguments asserts the command line is the
// positional arguments joined with single spaces, so an unquoted argument list
// and a quoted command are the same request.
func TestParseFlagsJoinsPositionalArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "one word", args: []string{"help"}, want: "help"},
		{name: "two words", args: []string{"help", "buoy"}, want: "help buoy"},
		{name: "many words", args: []string{"dist", "CM87uk", "CM87wj"}, want: "dist CM87uk CM87wj"},
		{name: "quoted command", args: []string{"help buoy"}, want: "help buoy"},
		{name: "leading and trailing space", args: []string{"  help  "}, want: "help"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts, err := parseFlags(tt.args, &bytes.Buffer{})
			if err != nil {
				t.Fatalf("parseFlags(%v): %v", tt.args, err)
			}
			if opts.message != tt.want {
				t.Errorf("message = %q, want %q", opts.message, tt.want)
			}
		})
	}
}

// TestParseFlagsOverrides asserts every flag is honored.
func TestParseFlagsOverrides(t *testing.T) {
	t.Parallel()

	opts, err := parseFlags([]string{
		"-dest", "28c7c1a68c735693aa8e6b8193ed44b2",
		"--to", "somebot",
		"--room", "ops",
		"--nick", "field-op",
		"--config", "/tmp/rns-alt",
		"--identity", "/tmp/rns-alt/storage/transport_identity",
		"--timeout", "5s",
		"--quiet", "250ms",
		"--verbose",
		"ping",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if opts.dest != "28c7c1a68c735693aa8e6b8193ed44b2" {
		t.Errorf("dest = %q", opts.dest)
	}
	if opts.target != "somebot" {
		t.Errorf("target = %q", opts.target)
	}
	if opts.room != "ops" {
		t.Errorf("room = %q", opts.room)
	}
	if opts.nick != "field-op" {
		t.Errorf("nick = %q", opts.nick)
	}
	if opts.configDir != "/tmp/rns-alt" {
		t.Errorf("configDir = %q", opts.configDir)
	}
	if opts.identity != "/tmp/rns-alt/storage/transport_identity" {
		t.Errorf("identity = %q", opts.identity)
	}
	if opts.timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", opts.timeout)
	}
	if opts.quiet != 250*time.Millisecond {
		t.Errorf("quiet = %v, want 250ms", opts.quiet)
	}
	if !opts.verbose {
		t.Error("verbose = false, want true")
	}
	if opts.message != "ping" {
		t.Errorf("message = %q", opts.message)
	}
}

// TestParseFlagsVersionNeedsNoCommand asserts --version parses without a
// positional command, because it exits before anything connects.
func TestParseFlagsVersionNeedsNoCommand(t *testing.T) {
	t.Parallel()

	opts, err := parseFlags([]string{"--version"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !opts.version {
		t.Error("version = false, want true")
	}
}

// TestParseFlagsErrors asserts every unusable command line is rejected with a
// message naming the problem, rather than failing later on the wire.
func TestParseFlagsErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantSub string
	}{
		{name: "no command", args: []string{}, wantSub: "no command given"},
		{name: "blank command", args: []string{"   "}, wantSub: "no command given"},
		{name: "destination too short", args: []string{"-dest", "a012129c", "help"}, wantSub: "32 hexadecimal"},
		{name: "destination not hex", args: []string{"-dest", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", "help"},
			wantSub: "invalid -dest"},
		{name: "empty target", args: []string{"--to", "  ", "help"}, wantSub: "target nick"},
		{name: "empty room", args: []string{"--room", "", "help"}, wantSub: "room"},
		{name: "zero timeout", args: []string{"--timeout", "0s", "help"}, wantSub: "timeout"},
		{name: "zero quiet", args: []string{"--quiet", "0s", "help"}, wantSub: "quiet"},
		{name: "unknown flag", args: []string{"--nope", "help"}, wantSub: "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseFlags(tt.args, &bytes.Buffer{})
			if err == nil {
				t.Fatalf("parseFlags(%v) = nil error, want a failure", tt.args)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("parseFlags(%v) error = %q, want it to contain %q", tt.args, err, tt.wantSub)
			}
		})
	}
}

// TestParseFlagsHelp asserts -h and --help return the help sentinel and print
// the usage text.
func TestParseFlagsHelp(t *testing.T) {
	t.Parallel()

	for _, flagName := range []string{"-h", "--help"} {
		t.Run(flagName, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			_, err := parseFlags([]string{flagName}, &buf)
			if !errors.Is(err, errHelp) {
				t.Fatalf("parseFlags(%v) error = %v, want errHelp", flagName, err)
			}
			if !strings.Contains(buf.String(), "usage: gobot") {
				t.Errorf("usage output = %q, want it to name the tool", buf.String())
			}
		})
	}
}

// TestUsageTextNamesTheLiveDefaults asserts the help text keeps naming the
// destination and nick it actually defaults to, so the documentation cannot
// drift from the flags.
func TestUsageTextNamesTheLiveDefaults(t *testing.T) {
	t.Parallel()

	for _, want := range []string{DefaultDestination, DefaultTargetNick, DefaultRoom, DefaultOwnNick, "-dest"} {
		if !strings.Contains(usageText, want) {
			t.Errorf("usageText does not mention %q", want)
		}
	}
}
