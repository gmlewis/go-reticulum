// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Command gorngcs is a Git commit signer and validator. It is a Go port of
// the Reticulum rngcs tool (RNS/Utilities/rngit/commitsigs.py) and produces
// SSHSIG-format signatures backed by Reticulum Signed Git (RSG) envelopes.
//
// Usage:
//
//	gorngcs -Y sign -f KEYFILE [-n NAMESPACE] [file]
//	gorngcs -Y find-principals -s SIGFILE
//	gorngcs -Y check-novalidate -s SIGFILE
//	gorngcs -Y verify -s SIGFILE [-I PRINCIPAL] [-f ALLOWED_SIGNERS]
//
// The -O option is accepted for git compatibility and ignored.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run executes the requested operation against the parsed arguments. It is
// split from main for testability. It returns the process exit code.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	switch a.op {
	case "sign":
		return a.sign(stdin, stdout, stderr)
	case "find-principals":
		return a.findPrincipals(stdout, stderr)
	case "check-novalidate":
		return a.checkNoValidate(stderr)
	case "verify":
		return a.verify(stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Error: Unknown operation: %s\n", a.op)
		return 1
	}
}
