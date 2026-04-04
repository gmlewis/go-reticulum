// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"flag"
	"fmt"
)

var errHelp = errors.New("help requested")

type appT struct {
	configDir     string
	table         bool
	maxHops       int
	rates         bool
	drop          bool
	dropAnnounces bool
	dropVia       bool
	timeout       float64
	jsonOut       bool
	verbose       bool
	version       bool
	args          []string
}

func parseFlags(args []string) (*appT, error) {
	app := &appT{timeout: 15.0}
	fs := flag.NewFlagSet("gornpath", flag.ContinueOnError)
	fs.SetOutput(flag.CommandLine.Output())
	fs.Usage = app.usage
	fs.StringVar(&app.configDir, "config", "", "path to alternative Reticulum config directory")
	fs.BoolVar(&app.table, "t", false, "show all known paths")
	fs.IntVar(&app.maxHops, "m", 0, "maximum hops to filter path table by")
	fs.BoolVar(&app.rates, "r", false, "show announce rate info")
	fs.BoolVar(&app.drop, "d", false, "remove the path to a destination")
	fs.BoolVar(&app.dropAnnounces, "D", false, "drop all queued announces")
	fs.BoolVar(&app.dropVia, "x", false, "drop all paths via specified transport instance")
	fs.Float64Var(&app.timeout, "w", 15.0, "timeout before giving up")
	fs.BoolVar(&app.jsonOut, "j", false, "output in JSON format")
	fs.BoolVar(&app.verbose, "v", false, "increase verbosity")
	fs.BoolVar(&app.version, "version", false, "show version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, err
	}
	app.args = append([]string{}, fs.Args()...)
	return app, nil
}

func (a *appT) usage() {
	_, _ = fmt.Fprintf(flag.CommandLine.Output(), `usage: gornpath [-h] [--config CONFIG] [-t] [-m MAX_HOPS] [-r] [-d] [-D]
                  [-x] [-w SECONDS] [-j] [-v] [--version] [destination]

Go Reticulum path management utility

options:
  -h, --help       show this help message and exit
  --config CONFIG  path to alternative Reticulum config directory
  -t               show all known paths in the routing table
  -m MAX_HOPS      maximum hops to filter path table by
  -r               show announce rate info
  -d               remove the path to a specified destination
  -D               drop all queued announces
  -x               drop all paths via specified transport instance
  -w SECONDS       timeout before giving up on a path request
  -j               output information in JSON format
  -v               increase verbosity
  --version        show program's version number and exit
`)
}
