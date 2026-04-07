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

func TestParseFlags(t *testing.T) {
	t.Parallel()
	opts, err := parseFlags([]string{"--config", "/tmp/config", "--identity", "/tmp/id", "--service", "svc", "--print-identity", "--listen", "--verbose", "-v", "--quiet", "--no-id", "--no-tty", "--mirror", "--timeout", "30", "--announce", "5", "--no-auth", "--remote-command-as-args", "--no-remote-command", "--allowed", "abc", "dest", "echo", "hi"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if opts.configDir != "/tmp/config" || opts.identityPath != "/tmp/id" || opts.serviceName != "svc" || !opts.printIdentity || !opts.listen || opts.verbose != 2 || opts.quiet != 1 || !opts.noID || !opts.noTTY || !opts.mirror || opts.timeoutSec != 30 || opts.announceEvery == nil || *opts.announceEvery != 5 || !opts.noAuth || !opts.remoteAsArgs || !opts.noRemoteCmd || len(opts.allowHashes) != 1 || opts.allowHashes[0] != "abc" || opts.destination != "" || len(opts.commandLine) != 3 {
		t.Fatalf("unexpected opts: %+v", opts)
	}
}

func TestParseFlagsAnnounceEvery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantNil bool
		want    int
	}{
		{name: "unset", args: []string{}, wantNil: true},
		{name: "startup only", args: []string{"--announce", "0"}, want: 0},
		{name: "periodic", args: []string{"--announce", "60"}, want: 60},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts, err := parseFlags(tc.args, io.Discard)
			if err != nil {
				t.Fatalf("parseFlags failed: %v", err)
			}
			if tc.wantNil {
				if opts.announceEvery != nil {
					t.Fatalf("announceEvery=%v, want nil", *opts.announceEvery)
				}
				return
			}
			if opts.announceEvery == nil {
				t.Fatal("announceEvery is nil, want non-nil")
			}
			if got := *opts.announceEvery; got != tc.want {
				t.Fatalf("announceEvery=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseFlagsInvalidNumericValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "invalid timeout", args: []string{"--timeout", "notanumber"}},
		{name: "invalid announce", args: []string{"--announce", "notanumber"}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseFlags(tc.args, io.Discard)
			if err == nil {
				t.Fatal("parseFlags returned nil error")
			}
			if strings.EqualFold(err.Error(), errHelp.Error()) {
				t.Fatalf("parseFlags returned help error: %v", err)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "invalid") {
				t.Fatalf("parseFlags error %q does not mention invalid", err)
			}
		})
	}
}

func TestParseFlagsCommandSeparator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		args            []string
		wantListen      bool
		wantDestination string
		wantCommand     []string
	}{
		{
			name:            "listener keeps program args",
			args:            []string{"-l", "--", "/bin/sh", "-c", "echo hi"},
			wantListen:      true,
			wantDestination: "",
			wantCommand:     []string{"/bin/sh", "-c", "echo hi"},
		},
		{
			name:            "initiator splits destination from command",
			args:            []string{"abcdef1234567890abcdef1234567890", "--", "echo", "hi"},
			wantListen:      false,
			wantDestination: "abcdef1234567890abcdef1234567890",
			wantCommand:     []string{"echo", "hi"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts, err := parseFlags(tc.args, io.Discard)
			if err != nil {
				t.Fatalf("parseFlags failed: %v", err)
			}
			if opts.listen != tc.wantListen {
				t.Fatalf("listen=%v, want %v", opts.listen, tc.wantListen)
			}
			if opts.destination != tc.wantDestination {
				t.Fatalf("destination=%q, want %q", opts.destination, tc.wantDestination)
			}
			if len(opts.commandLine) != len(tc.wantCommand) {
				t.Fatalf("commandLine=%v, want %v", opts.commandLine, tc.wantCommand)
			}
			for i, want := range tc.wantCommand {
				if opts.commandLine[i] != want {
					t.Fatalf("commandLine[%d]=%q, want %q", i, opts.commandLine[i], want)
				}
			}
		})
	}
}

func TestParseFlagsHelp(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	_, err := parseFlags([]string{"--help"}, &buf)
	if err == nil {
		t.Fatal("parseFlags returned nil error for --help")
	}
	if err != errHelp {
		t.Fatalf("parseFlags error = %v, want %v", err, errHelp)
	}
	wantSubstrings := []string{
		"-c DIR --config DIR",
		"-i FILE --identity FILE",
		"-s NAME --service NAME",
		"-p --print-identity",
		"-l --listen",
		"-b --announce PERIOD",
		"-a HASH --allowed HASH",
		"-n --no-auth",
		"-N --no-id",
		"-A --remote-command-as-args",
		"-C --no-remote-command",
		"-m --mirror",
		"-w TIME --timeout TIME",
		"-T --no-tty",
		"-q --quiet",
		"-v --verbose",
		"--version",
		"--help",
	}
	got := buf.String()
	if got == "" {
		t.Fatal("usage text was empty")
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Fatalf("usage text missing %q in:\n%s", want, got)
		}
	}
}
