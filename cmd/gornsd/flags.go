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
)

type appT struct {
	configDir     string
	verbose       bool
	quiet         bool
	service       bool
	exampleConfig bool
	version       bool
}

var errHelp = errors.New("help requested")

func parseFlags(args []string, usageOutput io.Writer) (*appT, error) {
	app := &appT{}
	fs := flag.NewFlagSet("gornsd", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		app.usage(usageOutput)
	}
	fs.StringVar(&app.configDir, "config", "", "path to alternative Reticulum config directory")
	fs.BoolVar(&app.verbose, "v", false, "increase verbosity")
	fs.BoolVar(&app.quiet, "q", false, "decrease verbosity")
	fs.BoolVar(&app.service, "s", false, "rnsd is running as a service and should log to file")
	fs.BoolVar(&app.exampleConfig, "exampleconfig", false, "print verbose configuration example to stdout and exit")
	fs.BoolVar(&app.version, "version", false, "show version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, err
	}
	return app, nil
}

func (a *appT) usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, `usage: gornsd [-h] [--config CONFIG] [-v] [-q] [-s] [--exampleconfig]
                  [--version]

Go Reticulum daemon

options:
  -h, --help       show this help message and exit
  --config CONFIG  path to alternative Reticulum config directory
  -v               increase verbosity
  -q               decrease verbosity
  -s               log to file as a service
  --exampleconfig  print verbose configuration example to stdout and exit
  --version        show program's version number and exit
`)
}
