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
	"os"

	"github.com/gmlewis/go-reticulum/utils"
)

var errHelp = errors.New("help requested")

type appT struct {
	configDir        string
	identityPath     string
	verbose          bool
	quiet            bool
	listenMode       bool
	fetchMode        bool
	noCompress       bool
	silent           bool
	allowFetch       bool
	jail             string
	savePath         string
	overwrite        bool
	announceInterval int
	allowed          []string
	noAuth           bool
	printIdentity    bool
	phyRates         bool
	timeoutSec       float64
	version          bool
	args             []string
}

func (a *appT) usage(w io.Writer) {
	utils.WriteText(w, usageText)
}

func parseFlags(args []string, usageOutput io.Writer) (*appT, error) {
	app := &appT{timeoutSec: 15.0, announceInterval: -1}
	fs := flag.NewFlagSet("gorncp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		app.usage(usageOutput)
	}
	fs.StringVar(&app.configDir, "config", "", "path to alternative Reticulum config directory")
	fs.StringVar(&app.identityPath, "i", "", "path to identity to use")
	fs.BoolVar(&app.verbose, "v", false, "increase verbosity")
	fs.BoolVar(&app.quiet, "q", false, "decrease verbosity")
	fs.BoolVar(&app.listenMode, "l", false, "listen for incoming transfer requests")
	fs.BoolVar(&app.fetchMode, "f", false, "fetch file from remote listener instead of sending")
	fs.BoolVar(&app.noCompress, "C", false, "disable automatic compression")
	fs.BoolVar(&app.noCompress, "no-compress", false, "disable automatic compression")
	fs.BoolVar(&app.silent, "S", false, "disable transfer progress output")
	fs.BoolVar(&app.silent, "silent", false, "disable transfer progress output")
	fs.BoolVar(&app.allowFetch, "F", false, "allow authenticated clients to fetch files")
	fs.BoolVar(&app.allowFetch, "allow-fetch", false, "allow authenticated clients to fetch files")
	fs.StringVar(&app.jail, "j", "", "restrict fetch requests to specified path")
	fs.StringVar(&app.jail, "jail", "", "restrict fetch requests to specified path")
	fs.StringVar(&app.savePath, "s", "", "save received files in specified path")
	fs.StringVar(&app.savePath, "save", "", "save received files in specified path")
	fs.BoolVar(&app.overwrite, "O", false, "allow overwriting received files")
	fs.BoolVar(&app.overwrite, "overwrite", false, "allow overwriting received files")
	fs.IntVar(&app.announceInterval, "b", -1, "announce interval (0=once, >0=seconds)")
	fs.Func("a", "allow identity hash", func(s string) error {
		app.allowed = append(app.allowed, s)
		return nil
	})
	fs.BoolVar(&app.noAuth, "n", false, "accept requests from anyone")
	fs.BoolVar(&app.noAuth, "no-auth", false, "accept requests from anyone")
	fs.BoolVar(&app.printIdentity, "p", false, "print identity and destination info and exit")
	fs.BoolVar(&app.printIdentity, "print-identity", false, "print identity and destination info and exit")
	fs.BoolVar(&app.phyRates, "P", false, "display physical layer transfer rates")
	fs.BoolVar(&app.phyRates, "phy-rates", false, "display physical layer transfer rates")
	fs.Float64Var(&app.timeoutSec, "w", 15.0, "sender timeout seconds")
	fs.BoolVar(&app.version, "version", false, "show version")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, err
	}
	app.args = append([]string{}, fs.Args()...)
	return app, nil
}

func printUsage() {
	_, _ = fmt.Fprint(os.Stdout, usageText)
}

func (a *appT) validate() error {
	for _, allowed := range a.allowed {
		if err := validateIdentityHash(allowed); err != nil {
			return err
		}
	}
	return nil
}

const usageText = `
usage: gorncp [-h] [--config path] [-v] [-q] [-S] [-l] [-C] [-F] [-f] [-j path] [-s path] [-O] [-b seconds]
              [-a allowed_hash] [-n] [-p] [-i identity] [-w seconds] [-P] [--version]
              [file] [destination]

Go Reticulum File Transfer Utility

positional arguments:
  file                  file to be transferred
  destination           hexadecimal hash of the receiver

options:
  -h, --help            show this help message and exit
  --config path         path to alternative Reticulum config directory
  -v, --verbose         increase verbosity
  -q, --quiet           decrease verbosity
  -S, --silent          disable transfer progress output
  -l, --listen          listen for incoming transfer requests
  -C, --no-compress     disable automatic compression
  -F, --allow-fetch     allow authenticated clients to fetch files
  -f, --fetch           fetch file from remote listener instead of sending
  -j path, --jail path  restrict fetch requests to specified path
  -s path, --save path  save received files in specified path
  -O, --overwrite       Allow overwriting received files, instead of adding postfix
  -b seconds            announce interval, 0 to only announce at startup
  -a allowed_hash       allow this identity (or add in ~/.rncp/allowed_identities)
  -n, --no-auth         accept requests from anyone
  -p, --print-identity  print identity and destination info and exit
  -i identity           path to identity to use
  -w seconds            sender timeout before giving up
  -P, --phy-rates       display physical layer transfer rates
  --version             show program's version number and exit
`
