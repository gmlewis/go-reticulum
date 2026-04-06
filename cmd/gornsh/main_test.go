// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

const tempDirPrefix = "gornsh-test-"

func TestParseAllowedIdentityHash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantOK  bool
		wantLen int
	}{
		{name: "valid lowercase", input: "00112233445566778899aabbccddeeff", wantOK: true, wantLen: 16},
		{name: "valid uppercase", input: "00112233445566778899AABBCCDDEEFF", wantOK: true, wantLen: 16},
		{name: "invalid hex", input: "not-hex", wantOK: false, wantLen: 0},
		{name: "wrong length short", input: "0011", wantOK: false, wantLen: 0},
		{name: "empty", input: "", wantOK: false, wantLen: 0},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseAllowedIdentityHash(tc.input)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tc.wantOK)
			}
			if len(got) != tc.wantLen {
				t.Fatalf("len=%v, want %v", len(got), tc.wantLen)
			}
		})
	}
}

func TestSplitAllowedFile(t *testing.T) {
	t.Parallel()

	input := "# comment\n  \n00112233445566778899aabbccddeeff\n aabbccddeeff00112233445566778899 \n"
	got := splitAllowedFile(input)
	want := []string{"00112233445566778899aabbccddeeff", "aabbccddeeff00112233445566778899"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitAllowedFile()=%v, want %v", got, want)
	}
}

func TestChooseCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		opts          options
		remoteCommand string
		want          []string
		wantErr       bool
	}{
		{
			name:          "no remote command uses base",
			opts:          options{commandLine: []string{"/bin/echo", "hello"}},
			remoteCommand: "",
			want:          []string{"/bin/echo", "hello"},
		},
		{
			name:          "remote command disabled with remote command errors",
			opts:          options{commandLine: []string{"/bin/echo"}, noRemoteCmd: true},
			remoteCommand: "id",
			wantErr:       true,
		},
		{
			name:          "remote command as args appends",
			opts:          options{commandLine: []string{"/bin/echo", "base"}, remoteAsArgs: true},
			remoteCommand: "one two",
			want:          []string{"/bin/echo", "base", "one", "two"},
		},
		{
			name:          "remote command uses shell by default",
			opts:          options{},
			remoteCommand: "echo hi",
			want:          []string{"/bin/sh", "-lc", "echo hi"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := chooseCommand(tc.opts, tc.remoteCommand)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("chooseCommand()=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseCommandResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		response   any
		wantExit   int
		wantStdout string
		wantStderr string
		wantErr    bool
	}{
		{
			name:       "valid response",
			response:   []any{true, int64(7), []byte("out"), []byte("err")},
			wantExit:   7,
			wantStdout: "out",
			wantStderr: "err",
		},
		{
			name:     "invalid response type",
			response: map[string]any{"bad": true},
			wantErr:  true,
		},
		{
			name:     "invalid exit code",
			response: []any{true, "nope", []byte("out"), []byte("err")},
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			exitCode, stdout, stderr, err := parseCommandResponse(tc.response)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if exitCode != tc.wantExit {
				t.Fatalf("exitCode=%v, want %v", exitCode, tc.wantExit)
			}
			if string(stdout) != tc.wantStdout {
				t.Fatalf("stdout=%q, want %q", string(stdout), tc.wantStdout)
			}
			if string(stderr) != tc.wantStderr {
				t.Fatalf("stderr=%q, want %q", string(stderr), tc.wantStderr)
			}
		})
	}
}

func TestJoinCommandArgs(t *testing.T) {
	t.Parallel()

	if got := joinCommandArgs(nil); got != "" {
		t.Fatalf("joinCommandArgs(nil)=%q, want empty", got)
	}

	if got := joinCommandArgs([]string{"echo", "hello", "world"}); got != "echo hello world" {
		t.Fatalf("joinCommandArgs()=%q, want %q", got, "echo hello world")
	}
}

func TestConfigureLogger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		verbose   bool
		quiet     bool
		wantLevel int
	}{
		{name: "default", wantLevel: rns.LogNotice},
		{name: "verbose", verbose: true, wantLevel: rns.LogVerbose},
		{name: "quiet", quiet: true, wantLevel: rns.LogWarning},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := configureLogger(tc.verbose, tc.quiet)
			if got := logger.GetLogLevel(); got != tc.wantLevel {
				t.Fatalf("log level=%v, want %v", got, tc.wantLevel)
			}
		})
	}
}

func TestNewRuntime(t *testing.T) {
	t.Parallel()
	rt := newRuntime(options{verbose: true})
	if rt == nil {
		t.Fatal("newRuntime returned nil")
	}
	if rt.logger == nil {
		t.Fatal("newRuntime returned nil logger")
	}
	if got := rt.logger.GetLogLevel(); got != rns.LogVerbose {
		t.Fatalf("log level=%v, want %v", got, rns.LogVerbose)
	}
}

func TestBuildAllowPolicyLogsThroughInjectedLogger(t *testing.T) {
	t.Parallel()

	var captured string
	logger := rns.NewLogger()
	logger.SetLogDest(rns.LogCallback)
	logger.SetLogCallback(func(msg string) {
		captured += msg
	})
	logger.SetLogLevel(rns.LogWarning)

	mode, allowed := buildAllowPolicy(logger, options{allowHashes: []string{"not-a-hash"}})

	if mode != rns.AllowList {
		t.Fatalf("mode=%v, want %v", mode, rns.AllowList)
	}
	if len(allowed) != 0 {
		t.Fatalf("allowed=%v, want empty", allowed)
	}
	if !strings.Contains(captured, "Ignoring invalid allowed identity hash") {
		t.Fatalf("missing invalid-hash warning in %q", captured)
	}
	if !strings.Contains(captured, "Authentication enabled but no allowed identities configured") {
		t.Fatalf("missing empty-policy warning in %q", captured)
	}
}
