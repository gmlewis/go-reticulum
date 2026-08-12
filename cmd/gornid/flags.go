// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"flag"
	"io"
	"strconv"

	"github.com/gmlewis/go-reticulum/utils"
)

// counter implements flag.Value for a counted flag (e.g. -v -v -v).
type counter int

func (c *counter) String() string { return strconv.Itoa(int(*c)) }
func (c *counter) Set(string) error {
	*c++
	return nil
}
func (c *counter) IsBoolFlag() bool { return true }

// stringListFlag implements flag.Value for a multi-value flag that
// accumulates repeated uses (e.g. -s a -s b) and, after Parse, also
// collects leftover positional args (e.g. -s a b c), matching Python's
// argparse nargs="*" behaviour for the sign/encrypt/decrypt/validate flags.
type stringListFlag struct {
	vals []string
	set  bool
}

func (s *stringListFlag) String() string {
	if s == nil {
		return ""
	}
	out := ""
	for i, v := range s.vals {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}

func (s *stringListFlag) Set(v string) error {
	s.vals = append(s.vals, v)
	s.set = true
	return nil
}

func (a *appT) usage(w io.Writer) {
	utils.WriteText(w, usageText)
}

func parseFlags(args []string, usageOutput io.Writer) (*appT, error) {
	app := newApp()
	fs := flag.NewFlagSet("gornid", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		app.usage(usageOutput)
	}
	app.initFlags(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, utils.ErrHelp
		}
		return nil, err
	}
	app.absorbPositionals(fs)
	return app, nil
}

// absorbPositionals appends leftover positional args (fs.Args()) to the
// single multi-value list flag that was set, matching Python's nargs="*"
// consumption of trailing positionals. At most one such list is expected
// per invocation (validate_args enforces a single op). The first element
// of each set list is mirrored into its scalar field so doFileOps keeps
// working.
func (a *appT) absorbPositionals(fs *flag.FlagSet) {
	lists := []*stringListFlag{&a.signList, &a.encryptList, &a.decryptList, &a.validateList}
	scalars := []*string{&a.signFile, &a.encryptFile, &a.decryptFile, &a.validateFile}

	// Append leftover positionals to the single set list, if any.
	rest := fs.Args()
	if len(rest) > 0 {
		var setIdx int = -1
		for i, l := range lists {
			if l.set {
				if setIdx != -1 {
					// Multiple list ops set: do not absorb positionals; the
					// dispatcher rejects multi-op invocations anyway.
					setIdx = -2
					break
				}
				setIdx = i
			}
		}
		if setIdx >= 0 {
			lists[setIdx].vals = append(lists[setIdx].vals, rest...)
		}
	}

	// Mirror the first element of each set list into its scalar field so
	// existing doFileOps single-file dispatch keeps working.
	for i, l := range lists {
		if l.set && len(l.vals) > 0 {
			*scalars[i] = l.vals[0]
		}
	}
}

func newApp() *appT { return &appT{timeout: 15.0} }

func (a *appT) initFlags(fs *flag.FlagSet) {
	fs.StringVar(&a.configDir, "config", "", "path to alternative Reticulum config directory")
	fs.StringVar(&a.identityPath, "i", "", "hexadecimal Reticulum identity or destination hash, or path to Identity file")
	fs.StringVar(&a.identityPath, "identity", "", "hexadecimal Reticulum identity or destination hash, or path to Identity file")
	fs.StringVar(&a.generatePath, "g", "", "generate a new Identity")
	fs.StringVar(&a.generatePath, "generate", "", "generate a new Identity")
	fs.StringVar(&a.importPub, "m", "", "import public Reticulum identity in hex, base32 or base64 format, or from file")
	fs.StringVar(&a.importPub, "import-pub", "", "import public Reticulum identity in hex, base32 or base64 format, or from file")
	fs.StringVar(&a.importPrv, "M", "", "import Reticulum identity in hex, base32 or base64 format, or from file")
	fs.StringVar(&a.importPrv, "import-prv", "", "import Reticulum identity in hex, base32 or base64 format, or from file")
	fs.BoolVar(&a.exportPub, "x", false, "export public identity to hex, base32 or base64 format")
	fs.BoolVar(&a.exportPub, "export-pub", false, "export public identity to hex, base32 or base64 format")
	fs.BoolVar(&a.exportPrv, "X", false, "export private identity to hex, base32 or base64 format, or to file")
	fs.BoolVar(&a.exportPrv, "export-prv", false, "export private identity to hex, base32 or base64 format, or to file")
	fs.Var(&a.verbose, "v", "increase verbosity")
	fs.Var(&a.verbose, "verbose", "increase verbosity")
	fs.Var(&a.quiet, "q", "decrease verbosity")
	fs.Var(&a.quiet, "quiet", "decrease verbosity")
	fs.StringVar(&a.announce, "a", "", "announce a destination based on this Identity")
	fs.StringVar(&a.announce, "announce", "", "announce a destination based on this Identity")
	fs.StringVar(&a.hashAspects, "H", "", "show destination hashes for other aspects for this Identity")
	fs.StringVar(&a.hashAspects, "hash", "", "show destination hashes for other aspects for this Identity")
	fs.Var(&a.encryptList, "e", "encrypt file (repeatable, accepts multiple paths)")
	fs.Var(&a.encryptList, "encrypt", "encrypt file (repeatable, accepts multiple paths)")
	fs.Var(&a.decryptList, "d", "decrypt file (repeatable, accepts multiple paths)")
	fs.Var(&a.decryptList, "decrypt", "decrypt file (repeatable, accepts multiple paths)")
	fs.Var(&a.signList, "s", "sign file (repeatable, accepts multiple paths)")
	fs.Var(&a.signList, "sign", "sign file (repeatable, accepts multiple paths)")
	fs.Var(&a.validateList, "V", "validate signature (repeatable, accepts multiple paths)")
	fs.Var(&a.validateList, "validate", "validate signature (repeatable, accepts multiple paths)")
	fs.StringVar(&a.signMessage, "S", "", "create embedded signed message (RSM)")
	fs.StringVar(&a.signMessage, "sign-message", "", "create embedded signed message (RSM)")
	fs.StringVar(&a.embedMeta, "E", "", "embed metadata structure from file")
	fs.StringVar(&a.embedMeta, "embed-meta", "", "embed metadata structure from file")
	fs.StringVar(&a.metaSpec, "meta-spec", "", "validate metadata for embedding with spec from file")
	fs.BoolVar(&a.meta, "meta", false, "Display RSM metadata if available")
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
	fs.BoolVar(&a.useBase256, "U", false, "Use base256-encoded input and output")
	fs.BoolVar(&a.useBase256, "base256", false, "Use base256-encoded input and output")
	fs.BoolVar(&a.useHex, "F", false, "Use hex-encoded input and output")
	fs.BoolVar(&a.useHex, "hex", false, "Use hex-encoded input and output")
	fs.BoolVar(&a.noCache, "N", false, "never use cached or network-sourced information")
	fs.BoolVar(&a.noCache, "no-cache", false, "never use cached or network-sourced information")
	fs.BoolVar(&a.useStdin, "I", false, "read input from STDIN")
	fs.BoolVar(&a.useStdin, "stdin", false, "read input from STDIN")
	fs.BoolVar(&a.useStdout, "O", false, "write output to STDOUT")
	fs.BoolVar(&a.useStdout, "stdout", false, "write output to STDOUT")
	fs.BoolVar(&a.rawSign, "raw", false, "sign raw input data instead of hashing first")
	fs.BoolVar(&a.version, "version", false, "show program's version number and exit")
}

const usageText = `
usage: gornid [-h] [--config path] [-i identity] [-g file] [-m identity_data] [-M identity_data]
              [-x] [-X] [-v] [-q] [-a aspects] [-H aspects] [-e file] [-d file] [-s path] [-V path]
              [-S text] [-E path] [--meta-spec path] [--meta] [-r file] [-w file] [-f] [-R] [-N]
              [-t seconds] [-p] [-P] [-b] [-B] [-U] [-F] [--raw] [--version]

Go Reticulum Identity & Encryption Utility

options:
  -h, --help            show this help message and exit
  --config path         path to alternative Reticulum config directory
  -i, --identity identity
                        hexadecimal Reticulum identity or destination hash, or path to Identity file
  -g, --generate file   generate a new Identity
  -m, --import-pub identity_data
                        import public Reticulum identity in hex, base32 or base64 format, or from file
  -M, --import-prv identity_data
                        import Reticulum identity in hex, base32 or base64 format, or from file
  -x, --export-pub      export public identity to hex, base32 or base64 format
  -X, --export-prv      export private identity to hex, base32 or base64 format, or to file
  -v, --verbose         increase verbosity
  -q, --quiet           decrease verbosity
  -a, --announce aspects
                        announce a destination based on this Identity
  -H, --hash aspects    show destination hashes for other aspects for this Identity
  -e, --encrypt file    encrypt file (repeatable, accepts multiple paths)
  -d, --decrypt file    decrypt file (repeatable, accepts multiple paths)
  -s, --sign path       sign file (repeatable, accepts multiple paths)
  -V, --validate path   validate signature (repeatable, accepts multiple paths)
  -S, --sign-message text
                        create embedded signed message (RSM)
  -E, --embed-meta path embed metadata structure from file
  --meta-spec path      validate metadata for embedding with spec from file
  --meta                Display RSM metadata if available
  -r, --read file       input file path
  -w, --write file      output file path
  -f, --force           write output even if it overwrites existing files
  -R, --request         request unknown Identities from the network
  -N, --no-cache        never use cached or network-sourced information
  -t seconds            identity request timeout before giving up
  -p, --print-identity  print identity info and exit
  -P, --print-private   allow displaying private keys
  -b, --base64          Use base64-encoded input and output
  -B, --base32          Use base32-encoded input and output
  -U, --base256         Use base256-encoded input and output
  -F, --hex             Use hex-encoded input and output
  --raw                 sign raw input data instead of hashing first
  --version             show program's version number and exit
`
