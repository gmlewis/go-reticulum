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

type appT struct {
	configDir     string
	verbose       counter
	quiet         counter
	exampleConfig bool
	version       bool
}

func newApp() *appT { return &appT{} }

func (a *appT) usage() {
	_, _ = fmt.Fprintf(flag.CommandLine.Output(), `
usage: gornir [-h] [--config CONFIG] [-v] [-q] [--exampleconfig] [--version]

Go Reticulum Distributed Identity Resolver

options:
  -h, --help       show this help message and exit
  --config CONFIG  path to alternative Reticulum config directory
  -v, --verbose    increase verbosity
  -q, --quiet      decrease verbosity
  --exampleconfig  print verbose configuration example to stdout and exit
  --version        show program's version number and exit
`)
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
