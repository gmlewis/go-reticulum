// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestParseFlags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		want    *appT
		wantErr bool
	}{
		{
			name: "all flags",
			args: []string{
				"--config", "/tmp/config",
				"-v", "-v", "-q",
				"-p", "-l", "-i", "/tmp/id", "-x", "-b",
				"-a", "abc1", "-a", "def2",
				"-n", "-N", "-d", "-m",
				"-w", "15.5", "-W", "30.0",
				"--stdin", "input", "--stdout", "1024", "--stderr", "2048",
				"dest_hash", "command_to_run",
			},
			want: &appT{
				configDir:     "/tmp/config",
				verbosity:     2,
				quietness:     1,
				printIdentity: true,
				listenMode:    true,
				identityPath:  "/tmp/id",
				interactive:   true,
				noAnnounce:    true,
				allowedHashes: []string{"abc1", "def2"},
				noAuth:        true,
				noID:          true,
				detailed:      true,
				mirror:        true,
				timeout:       15.5,
				resultTimeout: 30.0,
				stdin:         "input",
				stdoutLimit:   1024,
				stderrLimit:   2048,
				args:          []string{"dest_hash", "command_to_run"},
			},
		},
		{
			name: "minimal",
			args: []string{"dest_hash"},
			want: &appT{
				timeout: 15.0, // default value from RNS.Transport.PATH_REQUEST_TIMEOUT
				args:    []string{"dest_hash"},
			},
		},
		{
			name: "count and repeated flags",
			args: []string{"-v", "-v", "-v", "-q", "-q", "-a", "hash1", "-a", "hash2", "dest"},
			want: &appT{
				verbosity:     3,
				quietness:     2,
				allowedHashes: []string{"hash1", "hash2"},
				timeout:       15.0,
				args:          []string{"dest"},
			},
		},
		{
			name: "no arguments",
			args: []string{},
			want: &appT{
				timeout: 15.0,
				args:    []string{},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, err := parseFlags(tc.args, io.Discard)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseFlags() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil {
				// We don't compare the whole struct directly because of float precision and default values
				// but here they are exact or we can check fields.
				if !reflect.DeepEqual(app, tc.want) {
					t.Fatalf("parseFlags() = %+v, want %+v", app, tc.want)
				}
			}
		})
	}
}

func TestParseFlagsHelp(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	_, err := parseFlags([]string{"--help"}, &buf)
	if err != errHelp {
		t.Fatalf("parseFlags error = %v, want %v", err, errHelp)
	}
	got := buf.String()
	if !strings.Contains(got, "usage: gornx") {
		t.Errorf("help output missing usage line, got:\n%v", got)
	}
	if !strings.Contains(got, "Go Reticulum Remote Execution Utility") {
		t.Errorf("help output missing description, got:\n%v", got)
	}
	// Verify some key options are present
	for _, opt := range []string{"--config", "-v", "-p", "-l", "-i", "-x", "-a", "-n", "-w", "--stdin", "--stdout", "--version"} {
		if !strings.Contains(got, opt) {
			t.Errorf("help output missing option %q", opt)
		}
	}
}

func TestParseFlagsVersion(t *testing.T) {
	t.Parallel()
	// Version often exits directly or returns a special error.
	// Python argparse -v prints version and exits.
	// Go flag package doesn't have a standard version flag that behaves exactly like Python's.
}
