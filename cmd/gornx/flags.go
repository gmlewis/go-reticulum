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

const usageText = `
usage: gornx [-h] [--config CONFIG] [-i IDENTITY] [-v] [-q] [-l] [-x]
							 [destination_hash command]

Reticulum remote command execution compatible with rnx

options:
	-h, --help       show this help message and exit
	--config CONFIG  path to alternative Reticulum config directory
	-i IDENTITY      path to identity to use
	-v               increase verbosity
	-q               decrease verbosity
	-l               listen for incoming commands
	-x               enter interactive mode
`

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
