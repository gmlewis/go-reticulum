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

	"github.com/gmlewis/go-reticulum/rns"
)

type appT struct {
	logger        *rns.Logger
	configDir     string
	verbose       int
	quiet         int
	service       bool
	interactive   bool
	exampleConfig bool
	version       bool
}

var errHelp = errors.New("help requested")

type countFlag struct {
	target *int
}

func (f *countFlag) String() string {
	if f == nil || f.target == nil {
		return "0"
	}
	return fmt.Sprintf("%v", *f.target)
}

func (f *countFlag) Set(string) error {
	if f != nil && f.target != nil {
		*f.target = *f.target + 1
	}
	return nil
}

func (f *countFlag) IsBoolFlag() bool {
	return true
}

func parseFlags(args []string, usageOutput io.Writer) (*appT, error) {
	app := &appT{}
	fs := flag.NewFlagSet("gornsd", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		app.usage(usageOutput)
	}
	fs.StringVar(&app.configDir, "config", "", "path to alternative Reticulum config directory")
	fs.Var(&countFlag{target: &app.verbose}, "v", "")
	fs.Var(&countFlag{target: &app.verbose}, "verbose", "")
	fs.Var(&countFlag{target: &app.quiet}, "q", "")
	fs.Var(&countFlag{target: &app.quiet}, "quiet", "")
	fs.BoolVar(&app.service, "s", false, "rnsd is running as a service and should log to file")
	fs.BoolVar(&app.service, "service", false, "rnsd is running as a service and should log to file")
	fs.BoolVar(&app.interactive, "i", false, "drop into interactive shell after initialisation")
	fs.BoolVar(&app.interactive, "interactive", false, "drop into interactive shell after initialisation")
	fs.BoolVar(&app.exampleConfig, "exampleconfig", false, "print verbose configuration example to stdout and exit")
	fs.BoolVar(&app.version, "version", false, "show program's version number and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, err
	}
	if remaining := fs.Args(); len(remaining) > 0 {
		return nil, errors.New("unrecognized arguments: " + remaining[0])
	}
	return app, nil
}

func (a *appT) usage(w io.Writer) {
	_, _ = fmt.Fprint(w, usageText)
}

const usageText = `
usage: gornsd [-h] [--config CONFIG] [-v] [-q] [-s] [-i] [--exampleconfig] [--version]

Go Reticulum Network Stack Daemon

options:
  -h, --help         show this help message and exit
  --config CONFIG    path to alternative Reticulum config directory
  -v, --verbose
  -q, --quiet
  -s, --service      rnsd is running as a service and should log to file
  -i, --interactive  drop into interactive shell after initialisation
  --exampleconfig    print verbose configuration example to stdout and exit
  --version          show program's version number and exit
`
