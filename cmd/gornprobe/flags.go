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

type appT struct {
	configDir string
	size      int
	probes    int
	timeout   float64
	wait      float64
	verbose   bool
	version   bool
	args      []string
}

var errHelp = errors.New("help requested")

func parseFlags(args []string) (*appT, error) {
	app := &appT{size: DefaultProbeSize, probes: 1}
	fs := flag.NewFlagSet("gornprobe", flag.ContinueOnError)
	fs.SetOutput(flag.CommandLine.Output())
	fs.Usage = app.usage
	fs.StringVar(&app.configDir, "config", "", "path to alternative Reticulum config directory")
	fs.IntVar(&app.size, "s", DefaultProbeSize, "size of probe packet payload in bytes")
	fs.IntVar(&app.probes, "n", 1, "number of probes to send")
	fs.Float64Var(&app.timeout, "t", 0, "timeout before giving up")
	fs.Float64Var(&app.wait, "w", 0, "time between each probe")
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
	_, _ = fmt.Fprintf(flag.CommandLine.Output(), `usage: gornprobe [-h] [--config CONFIG] [-n COUNT] [-s SIZE] [-t SECONDS]
                  [-w SECONDS] [-v] [--version] full_name destination_hash

Go Reticulum connectivity probing utility

options:
  -h, --help       show this help message and exit
  --config CONFIG  path to alternative Reticulum config directory
  -n COUNT         number of probes to send
  -s SIZE          size of probe packet payload in bytes
  -t SECONDS       timeout before giving up
  -w SECONDS       time between each probe
  -v               increase verbosity
  --version        show program's version number and exit
`)
}
