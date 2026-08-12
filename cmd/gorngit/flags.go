// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

var errHelp = errors.New("help requested")

// subcommands is the set of recognised rngit subcommands, mirroring
// server.main's subcommands list (rngit v1.4.2).
var subcommands = map[string]bool{
	"node":    true,
	"create":  true,
	"release": true,
	"perms":   true,
	"work":    true,
	"fork":    true,
	"sync":    true,
	"mirror":  true,
}

type subcommand string

const (
	subNode    subcommand = "node"
	subCreate  subcommand = "create"
	subRelease subcommand = "release"
	subPerms   subcommand = "perms"
	subWork    subcommand = "work"
	subFork    subcommand = "fork"
	subSync    subcommand = "sync"
	subMirror  subcommand = "mirror"
)

// options holds the parsed gorngit flags. Fields that only apply to a single
// subcommand are grouped loosely; the dispatcher ignores irrelevant ones.
type options struct {
	subcommand   subcommand
	configDir    string
	rnsConfigDir string
	identityPath string
	verbose      int
	quiet        int
	version      bool

	// node (serve) flags.
	printIdentity bool
	service       bool
	interactive   bool

	// release flags.
	signer  string
	name    string
	local   bool
	offline bool

	// work / mirror flags.
	scope string
	title string
	docID int

	// positional arguments (meaning depends on subcommand).
	repository string
	operation  string
	target     string
	source     string
	remote     string
}

// parseFlags parses gorngit arguments, mirroring server.main's subcommand
// dispatch and per-subcommand argparse setup. The first argument is treated as
// the subcommand when it matches a known name; otherwise the subcommand
// defaults to "node" and the full argument list is parsed as node flags.
func parseFlags(args []string, usageOutput io.Writer) (options, error) {
	var opts options
	opts.subcommand = subNode

	rest := args
	if len(args) > 0 && subcommands[args[0]] {
		opts.subcommand = subcommand(args[0])
		rest = args[1:]
	}

	fs := flag.NewFlagSet("gorngit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { usage(usageOutput) }

	// Common flags shared by every subcommand.
	fs.StringVar(&opts.configDir, "config", "", "path to alternative config directory")
	fs.StringVar(&opts.rnsConfigDir, "rnsconfig", "", "path to alternative Reticulum config directory")
	fs.Var(&countFlag{target: &opts.verbose}, "v", "increase verbosity")
	fs.Var(&countFlag{target: &opts.verbose}, "verbose", "increase verbosity")
	fs.Var(&countFlag{target: &opts.quiet}, "q", "increase quietness")
	fs.Var(&countFlag{target: &opts.quiet}, "quiet", "increase quietness")
	fs.BoolVar(&opts.version, "version", false, "show version")

	switch opts.subcommand {
	case subNode:
		fs.BoolVar(&opts.printIdentity, "p", false, "print identity and destination info and exit")
		fs.BoolVar(&opts.printIdentity, "print-identity", false, "print identity and destination info and exit")
		fs.BoolVar(&opts.service, "s", false, "rngit is running as a service and should log to file")
		fs.BoolVar(&opts.service, "service", false, "rngit is running as a service and should log to file")
		fs.BoolVar(&opts.interactive, "i", false, "drop into interactive shell after initialisation")
		fs.BoolVar(&opts.interactive, "interactive", false, "drop into interactive shell after initialisation")
	case subCreate:
		fs.StringVar(&opts.identityPath, "i", "", "path to identity")
		fs.StringVar(&opts.identityPath, "identity", "", "path to identity")
	case subRelease:
		fs.StringVar(&opts.identityPath, "i", "", "path to release identity")
		fs.StringVar(&opts.identityPath, "identity", "", "path to release identity")
		fs.StringVar(&opts.signer, "s", "", "path to signing identity, if different from release identity")
		fs.StringVar(&opts.signer, "signer", "", "path to signing identity, if different from release identity")
		fs.StringVar(&opts.name, "n", "", "package name if different from repo name")
		fs.StringVar(&opts.name, "name", "", "package name if different from repo name")
		fs.BoolVar(&opts.local, "L", false, "generate release locally, but don't upload")
		fs.BoolVar(&opts.local, "local", false, "generate release locally, but don't upload")
		fs.BoolVar(&opts.offline, "o", false, "verify manifest locally, but don't fetch updates")
		fs.BoolVar(&opts.offline, "offline", false, "verify manifest locally, but don't fetch updates")
	case subPerms:
		fs.StringVar(&opts.identityPath, "i", "", "path to release identity")
		fs.StringVar(&opts.identityPath, "identity", "", "path to release identity")
	case subWork:
		fs.StringVar(&opts.identityPath, "i", "", "path to identity")
		fs.StringVar(&opts.identityPath, "identity", "", "path to identity")
		fs.StringVar(&opts.scope, "scope", "active", "document scope: active, completed or all")
		fs.StringVar(&opts.title, "t", "", "document title for create")
		fs.StringVar(&opts.title, "title", "", "document title for create")
		fs.IntVar(&opts.docID, "d", 0, "document ID")
		fs.IntVar(&opts.docID, "id", 0, "document ID")
	case subFork, subSync:
		fs.StringVar(&opts.identityPath, "i", "", "path to identity")
		fs.StringVar(&opts.identityPath, "identity", "", "path to identity")
	case subMirror:
		fs.StringVar(&opts.identityPath, "i", "", "path to identity")
		fs.StringVar(&opts.identityPath, "identity", "", "path to identity")
		fs.StringVar(&opts.scope, "scope", "active", "document scope: active, completed or all")
	}

	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options{}, errHelp
		}
		return options{}, err
	}

	positional := fs.Args()
	switch opts.subcommand {
	case subNode:
		// node takes no positional arguments.
	case subCreate:
		if len(positional) >= 1 {
			opts.repository = positional[0]
		}
	case subRelease:
		if len(positional) >= 1 {
			opts.repository = positional[0]
		}
		if len(positional) >= 2 {
			opts.operation = positional[1]
		}
		if len(positional) >= 3 {
			opts.target = positional[2]
		}
	case subPerms:
		if len(positional) >= 1 {
			opts.remote = positional[0]
		}
	case subWork:
		if len(positional) >= 1 {
			opts.repository = positional[0]
		}
		if len(positional) >= 2 {
			opts.operation = positional[1]
		}
	case subFork:
		if len(positional) >= 1 {
			opts.source = positional[0]
		}
		if len(positional) >= 2 {
			opts.target = positional[1]
		}
	case subSync:
		if len(positional) >= 1 {
			opts.repository = positional[0]
		}
	case subMirror:
		if len(positional) >= 1 {
			opts.source = positional[0]
		}
		if len(positional) >= 2 {
			opts.target = positional[1]
		}
	}

	return opts, nil
}

// countFlag is a flag.Value that increments a counter on each occurrence,
// mirroring argparse's action="count".
type countFlag struct {
	target *int
}

func (f *countFlag) String() string {
	if f == nil || f.target == nil {
		return "0"
	}
	return strconv.Itoa(*f.target)
}

func (f *countFlag) Set(string) error {
	if f != nil && f.target != nil {
		*f.target++
	}
	return nil
}

func (f *countFlag) IsBoolFlag() bool {
	return true
}

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, usageText)
}

var usageText = strings.TrimPrefix(`
Usage:
  gorngit node [--config DIR] [--rnsconfig DIR] [-p] [-s] [-i] [-v... | -q...]
  gorngit create [--config DIR] [--rnsconfig DIR] [-i PATH] <repository>
  gorngit release [--config DIR] [--rnsconfig DIR] [-i PATH] [-s PATH] [-n NAME]
                  [-L] [-o] [<repository> <operation> <target>]
  gorngit perms [--config DIR] [--rnsconfig DIR] [-i PATH] <remote>
  gorngit work [--config DIR] [--rnsconfig DIR] [-i PATH] [--scope SCOPE]
               [-t TITLE] [-d ID] [<repository> <operation>]
  gorngit fork [--config DIR] [--rnsconfig DIR] [-i PATH] <source> <target>
  gorngit sync [--config DIR] [--rnsconfig DIR] [-i PATH] <repository>
  gorngit mirror [--config DIR] [--rnsconfig DIR] [-i PATH] [--scope SCOPE]
                 <source> <target>
  gorngit --version
  gorngit -h | --help

Options:
  --config DIR          Path to alternative config directory
  --rnsconfig DIR       Path to alternative Reticulum config directory
  -i PATH --identity PATH   Path to identity
  -v --verbose          Increase verbosity (repeatable)
  -q --quiet            Increase quietness (repeatable)
  --version             Show version
  -h --help             Show this help

node:
  -p --print-identity   Print identity and destination info and exit
  -s --service          Run as a service (log to file)
  -i --interactive      Drop into interactive shell after initialisation

release:
  -s --signer PATH      Path to signing identity, if different from release identity
  -n --name NAME        Package name if different from repo name
  -L --local            Generate release locally, but don't upload
  -o --offline          Verify manifest locally, but don't fetch updates

work / mirror:
  --scope SCOPE         Document scope: active, completed or all
  -t --title TITLE      Document title for create
  -d --id ID            Document ID
`, "\n")
