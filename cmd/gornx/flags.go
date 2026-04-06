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
	configDir    string
	identityPath string
	verbose      bool
	quiet        bool
	listenMode   bool
	interactive  bool
	args         []string
}

var errHelp = errors.New("help requested")

func parseFlags(args []string, usageOutput io.Writer) (*appT, error) {
	app := &appT{}
	fs := flag.NewFlagSet("gornx", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		app.usage(usageOutput)
	}
	fs.StringVar(&app.configDir, "config", "", "path to alternative Reticulum config directory")
	fs.StringVar(&app.identityPath, "i", "", "path to identity to use")
	fs.BoolVar(&app.verbose, "v", false, "increase verbosity")
	fs.BoolVar(&app.quiet, "q", false, "decrease verbosity")
	fs.BoolVar(&app.listenMode, "l", false, "listen for incoming commands")
	fs.BoolVar(&app.interactive, "x", false, "enter interactive mode")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, err
	}
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
