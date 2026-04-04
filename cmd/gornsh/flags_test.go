// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"
)

func TestParseFlags(t *testing.T) {
	t.Parallel()
	opts, err := parseFlags([]string{"--config", "/tmp/config", "--identity", "/tmp/id", "--service", "svc", "--print-identity", "--listen", "--verbose", "--quiet", "--no-id", "--no-tty", "--mirror", "--timeout", "30", "--announce", "5", "--no-auth", "--remote-command-as-args", "--no-remote-command", "--allowed", "abc", "dest", "echo", "hi"})
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if opts.configDir != "/tmp/config" || opts.identityPath != "/tmp/id" || opts.serviceName != "svc" || !opts.printIdentity || !opts.listen || !opts.verbose || !opts.quiet || !opts.noID || !opts.noTTY || !opts.mirror || opts.timeoutSec != 30 || opts.announceEvery != 5 || !opts.noAuth || !opts.remoteAsArgs || !opts.noRemoteCmd || len(opts.allowHashes) != 1 || opts.allowHashes[0] != "abc" || opts.destination != "" || len(opts.commandLine) != 3 {
		t.Fatalf("unexpected opts: %+v", opts)
	}
}
