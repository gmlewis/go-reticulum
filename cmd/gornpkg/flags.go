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
)

// counter implements flag.Value for a counted flag (e.g. -v -v -v).
type counter int

func (c *counter) String() string { return strconv.Itoa(int(*c)) }
func (c *counter) Set(string) error {
	*c++
	return nil
}
func (c *counter) IsBoolFlag() bool { return true }

type appT struct {
	configDir     string
	verbose       counter
	quiet         counter
	exampleConfig bool
	version       bool
}

func newApp() *appT { return &appT{} }

var errHelp = errors.New("help requested")

func parseFlags(args []string, usageOutput io.Writer) (*appT, error) {
	app := newApp()
	fs := flag.NewFlagSet("gornpkg", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		app.usage(usageOutput)
	}
	app.initFlags(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, err
	}
	return app, nil
}

func (a *appT) usage(w io.Writer) {
	_, _ = io.WriteString(w, usageText)
}

func (a *appT) initFlags(fs *flag.FlagSet) {
	fs.StringVar(&a.configDir, "config", "", "path to alternative Reticulum config directory")
	fs.Var(&a.verbose, "v", "increase verbosity")
	fs.Var(&a.verbose, "verbose", "increase verbosity")
	fs.Var(&a.quiet, "q", "decrease verbosity")
	fs.Var(&a.quiet, "quiet", "decrease verbosity")
	fs.BoolVar(&a.exampleConfig, "exampleconfig", false, "print verbose configuration example to stdout and exit")
	fs.BoolVar(&a.version, "version", false, "show program's version number and exit")
}

const usageText = `
usage: gornpkg [-h] [--config CONFIG] [-v] [-q] [--exampleconfig] [--version]

Go Reticulum Meta Package Manager

options:
  -h, --help       show this help message and exit
  --config CONFIG  path to alternative Reticulum config directory
  -v, --verbose
  -q, --quiet
  --exampleconfig  print verbose configuration example to stdout and exit
  --version        show program's version number and exit
`
