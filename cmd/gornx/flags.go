// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"flag"
	"io"

	"github.com/gmlewis/go-reticulum/utils"
)

type appT struct {
	configDir     string
	verbosity     int
	quietness     int
	printIdentity bool
	listenMode    bool
	identityPath  string
	interactive   bool
	noAnnounce    bool
	allowedHashes []string
	noAuth        bool
	noID          bool
	detailed      bool
	mirror        bool
	timeout       float64
	resultTimeout float64
	stdin         string
	stdoutLimit   int
	stderrLimit   int
	version       bool
	args          []string
}

var errHelp = errors.New("help requested")
var errVersion = errors.New("version requested")

type countFlag int

func (c *countFlag) String() string {
	return ""
}

func (c *countFlag) Set(s string) error {
	if s == "true" {
		*c++
	}
	return nil
}

func (c *countFlag) IsBoolFlag() bool {
	return true
}

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return ""
}

func (s *stringSliceFlag) Set(val string) error {
	*s = append(*s, val)
	return nil
}

func parseFlags(args []string, usageOutput io.Writer) (*appT, error) {
	app := &appT{
		timeout:     15.0, // RNS.Transport.PATH_REQUEST_TIMEOUT
		stdoutLimit: -1,   // No limit by default
		stderrLimit: -1,   // No limit by default
	}
	fs := flag.NewFlagSet("gornx", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		app.usage(usageOutput)
	}

	var vCount countFlag
	var qCount countFlag
	var allowed stringSliceFlag

	fs.StringVar(&app.configDir, "config", "", "")
	fs.Var(&vCount, "v", "")
	fs.Var(&qCount, "q", "")
	fs.BoolVar(&app.printIdentity, "p", false, "")
	fs.BoolVar(&app.listenMode, "l", false, "")
	fs.StringVar(&app.identityPath, "i", "", "")
	fs.BoolVar(&app.interactive, "x", false, "")
	fs.BoolVar(&app.noAnnounce, "b", false, "")
	fs.Var(&allowed, "a", "")
	fs.BoolVar(&app.noAuth, "n", false, "")
	fs.BoolVar(&app.noID, "N", false, "")
	fs.BoolVar(&app.detailed, "d", false, "")
	fs.BoolVar(&app.mirror, "m", false, "")
	fs.Float64Var(&app.timeout, "w", 15.0, "")
	fs.Float64Var(&app.resultTimeout, "W", 0, "")
	fs.StringVar(&app.stdin, "stdin", "", "")
	fs.IntVar(&app.stdoutLimit, "stdout", -1, "")
	fs.IntVar(&app.stderrLimit, "stderr", -1, "")
	fs.BoolVar(&app.version, "version", false, "")

	// Long names for compatibility
	fs.BoolVar(&app.printIdentity, "print-identity", false, "")
	fs.BoolVar(&app.listenMode, "listen", false, "")
	fs.BoolVar(&app.interactive, "interactive", false, "")
	fs.BoolVar(&app.noAnnounce, "no-announce", false, "")
	fs.BoolVar(&app.noAuth, "noauth", false, "")
	fs.BoolVar(&app.noID, "noid", false, "")
	fs.BoolVar(&app.detailed, "detailed", false, "")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, err
	}

	if app.version {
		return nil, errVersion
	}

	app.verbosity = int(vCount)
	app.quietness = int(qCount)
	app.allowedHashes = []string(allowed)
	app.args = append([]string{}, fs.Args()...)

	return app, nil
}

func (a *appT) usage(w io.Writer) {
	utils.WriteText(w, usageText)
}

const usageText = `
usage: gornx [-h] [--config path] [-v] [-q] [-p] [-l] [-i identity] [-x] [-b] [-a allowed_hash] [-n] [-N] [-d]
             [-m] [-w seconds] [-W seconds] [--stdin STDIN] [--stdout STDOUT] [--stderr STDERR] [--version]
             [destination] [command]

Go Reticulum Remote Execution Utility

positional arguments:
  destination           hexadecimal hash of the listener
  command               command to be execute

options:
  -h, --help            show this help message and exit
  --config path         path to alternative Reticulum config directory
  -v, --verbose         increase verbosity
  -q, --quiet           decrease verbosity
  -p, --print-identity  print identity and destination info and exit
  -l, --listen          listen for incoming commands
  -i identity           path to identity to use
  -x, --interactive     enter interactive mode
  -b, --no-announce     don't announce at program start
  -a allowed_hash       accept from this identity
  -n, --noauth          accept commands from anyone
  -N, --noid            don't identify to listener
  -d, --detailed        show detailed result output
  -m                    mirror exit code of remote command
  -w seconds            connect and request timeout before giving up
  -W seconds            max result download time
  --stdin STDIN         pass input to stdin
  --stdout STDOUT       max size in bytes of returned stdout
  --stderr STDERR       max size in bytes of returned stderr
  --version             show program's version number and exit
`
