// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"io"
	"testing"
)

func TestParseFlags(t *testing.T) {
	t.Parallel()
	opts, err := parseFlags([]string{"--config", "/tmp/config", "--identity", "/tmp/id", "--service", "svc", "--print-identity", "--listen", "--verbose", "--quiet", "--no-id", "--no-tty", "--mirror", "--timeout", "30", "--announce", "5", "--no-auth", "--remote-command-as-args", "--no-remote-command", "--allowed", "abc", "dest", "echo", "hi"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if opts.configDir != "/tmp/config" || opts.identityPath != "/tmp/id" || opts.serviceName != "svc" || !opts.printIdentity || !opts.listen || !opts.verbose || !opts.quiet || !opts.noID || !opts.noTTY || !opts.mirror || opts.timeoutSec != 30 || opts.announceEvery != 5 || !opts.noAuth || !opts.remoteAsArgs || !opts.noRemoteCmd || len(opts.allowHashes) != 1 || opts.allowHashes[0] != "abc" || opts.destination != "" || len(opts.commandLine) != 3 {
		t.Fatalf("unexpected opts: %+v", opts)
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
	usage(&buf)
	if buf.Len() == 0 {
		t.Fatal("usage text was empty")
	}
}
