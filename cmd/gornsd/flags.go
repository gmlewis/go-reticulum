// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

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

func parseFlags(args []string, usageOutput io.Writer) (*appT, error) {
	app := &appT{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-h", "--help":
			app.usage(usageOutput)
			return nil, errHelp
		case "--config":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("unrecognized arguments: --config")
			}
			app.configDir = args[i]
		case "-v", "--verbose":
			app.verbose++
		case "-q", "--quiet":
			app.quiet++
		case "-s", "--service":
			app.service = true
		case "-i", "--interactive":
			app.interactive = true
		case "--exampleconfig":
			app.exampleConfig = true
		case "--version":
			app.version = true
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unrecognized arguments: %v", arg)
			}
			return nil, fmt.Errorf("unrecognized arguments: %v", arg)
		}
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
