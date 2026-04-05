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
	configDir      string
	table          bool
	maxHops        int
	rates          bool
	drop           bool
	dropVia        bool
	dropAnnounces  bool
	blackholed     bool
	blackhole      bool
	unblackhole    bool
	blackholedList bool
	identityPath   string
	remoteHash     string
	duration       float64
	reason         string
	remoteTimeout  float64
	timeout        float64
	jsonOut        bool
	verbose        bool
	version        bool
	args           []string
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
	fs.BoolVar(&app.blackholed, "b", false, "show locally blackholed identities")
	fs.BoolVar(&app.blackhole, "B", false, "blackhole an identity")
	fs.BoolVar(&app.unblackhole, "U", false, "remove an identity from the blackhole list")
	fs.BoolVar(&app.blackholedList, "p", false, "show blackholed identities published by a remote instance")
	fs.StringVar(&app.identityPath, "i", "", "path to identity to use for remote access")
	fs.StringVar(&app.remoteHash, "R", "", "remote transport instance hash")
	fs.Float64Var(&app.remoteTimeout, "W", 10.0, "timeout before giving up on remote requests")
	fs.Float64Var(&app.duration, "duration", 0.0, "duration in hours for blackhole entries")
	fs.StringVar(&app.reason, "reason", "", "reason for blackhole entries")
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
				  [-x] [-b] [-B] [-U] [-p] [-i IDENTITY] [-R HASH]
				  [-W SECONDS] [--duration HOURS] [--reason TEXT]
				  [-w SECONDS] [-j] [-v] [--version] [destination]

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
	-b               show locally blackholed identities
	-B               blackhole an identity
	-U               remove an identity from the blackhole list
	-p               show blackholed identities published by a remote instance
	-i IDENTITY      path to identity to use for remote access
	-R HASH          remote transport instance hash
	-W SECONDS       timeout before giving up on remote requests
	--duration HOURS  duration in hours for blackhole entries
	--reason TEXT     reason for blackhole entries
  -w SECONDS       timeout before giving up on a path request
  -j               output information in JSON format
  -v               increase verbosity
  --version        show program's version number and exit
`)
}
