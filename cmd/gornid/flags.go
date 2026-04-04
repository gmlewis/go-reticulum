// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"strconv"
)

// counter implements flag.Value for a counted flag (e.g. -v -v -v).
type counter int

func (c *counter) String() string { return strconv.Itoa(int(*c)) }
func (c *counter) Set(string) error {
	*c++
	return nil
}
func (c *counter) IsBoolFlag() bool { return true }

func newApp() *appT { return &appT{timeout: 15.0} }

func (a *appT) usage() {
	_, _ = fmt.Fprint(flag.CommandLine.Output(), usageText)
}

func (a *appT) initFlags(fs *flag.FlagSet) {
	fs.StringVar(&a.configDir, "config", "", "path to alternative Reticulum config directory")
	fs.StringVar(&a.identityPath, "i", "", "hexadecimal Reticulum identity or destination hash, or path to Identity file")
	fs.StringVar(&a.identityPath, "identity", "", "hexadecimal Reticulum identity or destination hash, or path to Identity file")
	fs.StringVar(&a.generatePath, "g", "", "generate a new Identity")
	fs.StringVar(&a.generatePath, "generate", "", "generate a new Identity")
	fs.StringVar(&a.importStr, "m", "", "import Reticulum identity in hex, base32 or base64 format")
	fs.StringVar(&a.importStr, "import", "", "import Reticulum identity in hex, base32 or base64 format")
	fs.BoolVar(&a.export, "x", false, "export identity to hex, base32 or base64 format")
	fs.BoolVar(&a.export, "export", false, "export identity to hex, base32 or base64 format")
	fs.Var(&a.verbose, "v", "increase verbosity")
	fs.Var(&a.verbose, "verbose", "increase verbosity")
	fs.Var(&a.quiet, "q", "decrease verbosity")
	fs.Var(&a.quiet, "quiet", "decrease verbosity")
	fs.StringVar(&a.announce, "a", "", "announce a destination based on this Identity")
	fs.StringVar(&a.announce, "announce", "", "announce a destination based on this Identity")
	fs.StringVar(&a.hashAspects, "H", "", "show destination hashes for other aspects for this Identity")
	fs.StringVar(&a.hashAspects, "hash", "", "show destination hashes for other aspects for this Identity")
	fs.StringVar(&a.encryptFile, "e", "", "encrypt file")
	fs.StringVar(&a.encryptFile, "encrypt", "", "encrypt file")
	fs.StringVar(&a.decryptFile, "d", "", "decrypt file")
	fs.StringVar(&a.decryptFile, "decrypt", "", "decrypt file")
	fs.StringVar(&a.signFile, "s", "", "sign file")
	fs.StringVar(&a.signFile, "sign", "", "sign file")
	fs.StringVar(&a.validateFile, "V", "", "validate signature")
	fs.StringVar(&a.validateFile, "validate", "", "validate signature")
	fs.StringVar(&a.readFile, "r", "", "input file path")
	fs.StringVar(&a.readFile, "read", "", "input file path")
	fs.StringVar(&a.writeFile, "w", "", "output file path")
	fs.StringVar(&a.writeFile, "write", "", "output file path")
	fs.BoolVar(&a.force, "f", false, "write output even if it overwrites existing files")
	fs.BoolVar(&a.force, "force", false, "write output even if it overwrites existing files")
	fs.BoolVar(&a.requestID, "R", false, "request unknown Identities from the network")
	fs.BoolVar(&a.requestID, "request", false, "request unknown Identities from the network")
	fs.Float64Var(&a.timeout, "t", 15.0, "identity request timeout before giving up")
	fs.BoolVar(&a.printIdentity, "p", false, "print identity info and exit")
	fs.BoolVar(&a.printIdentity, "print-identity", false, "print identity info and exit")
	fs.BoolVar(&a.printPrivate, "P", false, "allow displaying private keys")
	fs.BoolVar(&a.printPrivate, "print-private", false, "allow displaying private keys")
	fs.BoolVar(&a.useBase64, "b", false, "Use base64-encoded input and output")
	fs.BoolVar(&a.useBase64, "base64", false, "Use base64-encoded input and output")
	fs.BoolVar(&a.useBase32, "B", false, "Use base32-encoded input and output")
	fs.BoolVar(&a.useBase32, "base32", false, "Use base32-encoded input and output")
	fs.BoolVar(&a.useStdin, "I", false, "read input from STDIN")
	fs.BoolVar(&a.useStdin, "stdin", false, "read input from STDIN")
	fs.BoolVar(&a.useStdout, "O", false, "write output to STDOUT")
	fs.BoolVar(&a.useStdout, "stdout", false, "write output to STDOUT")
	fs.BoolVar(&a.version, "version", false, "show program's version number and exit")
}
