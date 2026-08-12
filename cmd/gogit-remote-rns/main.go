// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Command git-remote-rns is the git remote-helper bridge that lets standard
// git clients clone, fetch, and push over Reticulum, mirroring
// RNS/Utilities/rngit/client.py (rngit v1.4.2).
//
// Git invokes the helper as `git-remote-rns <remote-name> <url>` where url is
// `rns://<hash>/<group>/<repo>`. The helper reads the git remote-helper
// protocol from stdin and writes responses to stdout, driving an RNS client
// (client.go) that talks to a gorngit repository node's /git/list, /git/fetch,
// /git/push, and /git/delete request handlers.
//
// Configuration directories come from the RNGIT_CONFIG (client identity and
// config) and RNS_CONFIG (Reticulum transport) environment variables, falling
// back to ~/.rngit and the default Reticulum config when unset.
package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/gmlewis/go-reticulum/rns"
)

func main() {
	log.SetFlags(0)
	os.Exit(run(os.Args[1:]))
}

// run is the testable entry point for git-remote-rns, mirroring client.py
// main() + program_setup. It parses args, initializes RNS, builds the client,
// wires a remoteHelper with the client as the helperBackend, runs the
// protocol loop, and returns the exit code (255 on abort, mirroring
// client.py abort's sys.exit(255)).
func run(args []string) int {
	return runWithOutput(args, os.Stdin, os.Stdout, os.Stderr)
}

// runWithOutput is the testable form of run, reading the helper protocol from
// in and writing protocol responses to out and diagnostics to errw.
func runWithOutput(args []string, in io.Reader, out, errw io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintf(errw, "Usage: git-remote-rns <remote-name> <url>\n")
		return 1
	}
	url := args[1]
	if !isRnsURL(url) {
		fmt.Fprintf(errw, "Invalid URL scheme. Must be rns://\n")
		return 1
	}

	configDir := os.Getenv("RNGIT_CONFIG")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(errw, "Could not determine home dir: %s\n", err)
			return 1
		}
		configDir = filepath.Join(home, ".rngit")
	}
	rnsConfigDir := os.Getenv("RNS_CONFIG")

	logger := rns.NewLogger()
	// The helper's stdout is the git remote-helper protocol channel and must
	// stay pristine. The rns logger defaults to stdout, so route every log
	// event to stderr via a callback instead (mirroring client.py writing
	// diagnostics to sys.stderr while keeping git_stdout clean).
	logger.SetLogCallback(func(msg string) {
		fmt.Fprintln(os.Stderr, msg)
	})
	logger.SetLogDest(rns.LogCallback)
	logger.SetLogLevel(rns.LogCritical)

	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, rnsConfigDir, logger)
	if err != nil {
		fmt.Fprintf(errw, "Failed to initialize Reticulum: %s\n", err)
		return 255
	}
	defer func() {
		if err := ret.Close(); err != nil {
			logger.Warning("Could not close Reticulum: %s", err)
		}
	}()

	client, err := newRnsClient(ts, configDir, url, logger)
	if err != nil {
		fmt.Fprintf(errw, "%s\n", err)
		return 255
	}

	if err := client.connect(logger); err != nil {
		fmt.Fprintf(errw, "git-remote-rns failed: %s\n", err)
		client.teardown()
		return 255
	}
	defer client.teardown()

	helper := &remoteHelper{
		in:      in,
		out:     bufio.NewWriter(out),
		errw:    errw,
		backend: client,
	}
	if err := helper.run(); err != nil {
		fmt.Fprintf(errw, "git-remote-rns failed: %s\n", err)
		return 255
	}
	return 0
}

// isRnsURL reports whether url begins with the rns:// scheme
// (case-insensitively), mirroring the url.startswith("rns://") check in
// client.py main().
func isRnsURL(url string) bool {
	return len(url) >= len(protoSpec) && equalFold(url[:len(protoSpec)], protoSpec)
}

// equalFold reports whether a and b are equal under ASCII case-folding.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
