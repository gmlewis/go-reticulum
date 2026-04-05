// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"flag"
	"io"
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

func parseFlags(args []string, usageOutput io.Writer) (*appT, error) {
	app := &appT{timeout: 15.0}
	fs := flag.NewFlagSet("gornpath", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		app.usage(usageOutput)
	}
	fs.StringVar(&app.configDir, "config", "", "path to alternative Reticulum config directory")
	fs.BoolVar(&app.table, "t", false, "show all known paths")
	fs.BoolVar(&app.table, "table", false, "show all known paths")
	fs.IntVar(&app.maxHops, "m", 0, "maximum hops to filter path table by")
	fs.IntVar(&app.maxHops, "max", 0, "maximum hops to filter path table by")
	fs.BoolVar(&app.rates, "r", false, "show announce rate info")
	fs.BoolVar(&app.rates, "rates", false, "show announce rate info")
	fs.BoolVar(&app.drop, "d", false, "remove the path to a destination")
	fs.BoolVar(&app.drop, "drop", false, "remove the path to a destination")
	fs.BoolVar(&app.dropAnnounces, "D", false, "drop all queued announces")
	fs.BoolVar(&app.dropAnnounces, "drop-announces", false, "drop all queued announces")
	fs.BoolVar(&app.dropVia, "x", false, "drop all paths via specified transport instance")
	fs.BoolVar(&app.dropVia, "drop-via", false, "drop all paths via specified transport instance")
	fs.BoolVar(&app.blackholed, "b", false, "show locally blackholed identities")
	fs.BoolVar(&app.blackholed, "blackholed", false, "show locally blackholed identities")
	fs.BoolVar(&app.blackhole, "B", false, "blackhole an identity")
	fs.BoolVar(&app.blackhole, "blackhole", false, "blackhole an identity")
	fs.BoolVar(&app.unblackhole, "U", false, "remove an identity from the blackhole list")
	fs.BoolVar(&app.unblackhole, "unblackhole", false, "remove an identity from the blackhole list")
	fs.BoolVar(&app.blackholedList, "p", false, "show blackholed identities published by a remote instance")
	fs.BoolVar(&app.blackholedList, "blackholed-list", false, "show blackholed identities published by a remote instance")
	fs.StringVar(&app.identityPath, "i", "", "path to identity to use for remote access")
	fs.StringVar(&app.identityPath, "identity", "", "path to identity to use for remote access")
	fs.StringVar(&app.remoteHash, "R", "", "remote transport instance hash")
	fs.StringVar(&app.remoteHash, "remote", "", "remote transport instance hash")
	fs.Float64Var(&app.remoteTimeout, "W", 10.0, "timeout before giving up on remote requests")
	fs.Float64Var(&app.duration, "duration", 0.0, "duration in hours for blackhole entries")
	fs.StringVar(&app.reason, "reason", "", "reason for blackhole entries")
	fs.Float64Var(&app.timeout, "w", 15.0, "timeout before giving up")
	fs.BoolVar(&app.jsonOut, "j", false, "output in JSON format")
	fs.BoolVar(&app.jsonOut, "json", false, "output in JSON format")
	fs.BoolVar(&app.verbose, "v", false, "increase verbosity")
	fs.BoolVar(&app.verbose, "verbose", false, "increase verbosity")
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

func (a *appT) usage(w io.Writer) {
	_, _ = io.WriteString(w, usageText)
}

const usageText = `

usage: gornpath [-h] [--config CONFIG] [--version] [-t] [-m hops] [-r] [-d] [-D] [-x] [-w seconds] [-R hash]
                [-i path] [-W seconds] [-b] [-B] [-U] [--duration DURATION] [--reason REASON] [-p] [-j] [-v]
                [destination] [list_filter]

Go Reticulum Path Management Utility

positional arguments:
  destination           hexadecimal hash of the destination
  list_filter           filter for remote blackhole list view

options:
  -h, --help            show this help message and exit
  --config CONFIG       path to alternative Reticulum config directory
  --version             show program's version number and exit
  -t, --table           show all known paths
  -m hops, --max hops   maximum hops to filter path table by
  -r, --rates           show announce rate info
  -d, --drop            remove the path to a destination
  -D, --drop-announces  drop all queued announces
  -x, --drop-via        drop all paths via specified transport instance
  -w seconds            timeout before giving up
  -R hash               transport identity hash of remote instance to manage
  -i path               path to identity used for remote management
  -W seconds            timeout before giving up on remote queries
  -b, --blackholed      list blackholed identities
  -B, --blackhole       blackhole identity
  -U, --unblackhole     unblackhole identity
  --duration DURATION   duration of blackhole enforcement in hours
  --reason REASON       reason for blackholing identity
  -p, --blackholed-list
                        view published blackhole list for remote transport instance
  -j, --json            output in JSON format
  -v, --verbose
`
