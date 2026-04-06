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
	"strconv"
	"time"

	"github.com/gmlewis/go-reticulum/utils"
)

const usageText = `
usage: golxmd [-h] [--config CONFIG] [--rnsconfig RNSCONFIG] [-p] [-i PATH] [-v] [-q] [-s]
              [--status] [--peers] [--sync SYNC] [-b UNPEER] [--timeout TIMEOUT] [-r REMOTE]
              [--identity IDENTITY] [--exampleconfig] [--version]

Go Lightweight Extensible Messaging Daemon

options:
  -h, --help            show this help message and exit
  --config CONFIG       path to alternative golxmd config directory
  --rnsconfig RNSCONFIG
                        path to alternative Reticulum config directory
  -p, --propagation-node
                        run an LXMF Propagation Node
  -i, --on-inbound PATH
                        executable to run when a message is received
  -v, --verbose
  -q, --quiet
  -s, --service         golxmd is running as a service and should log to file
  --status              display node status
  --peers               display peered nodes
  --sync SYNC           request a sync with the specified peer
  -b, --break UNPEER    break peering with the specified peer
  --timeout TIMEOUT     timeout in seconds for query operations
  -r, --remote REMOTE   remote propagation node destination hash
  --identity IDENTITY   path to identity used for remote requests
  --exampleconfig       print verbose configuration example to stdout and exit
  --version             show program's version number and exit
`

type appT struct {
	configDir            string
	rnsConfigDir         string
	runAsPropagationNode bool
	cmdOnInbound         string
	verbosity            countFlag
	quietness            countFlag
	runAsService         bool
	displayStatus        bool
	displayPeers         bool
	syncHash             string
	unpeerHash           string
	timeout              time.Duration
	remoteHash           string
	identityPath         string
	exampleConfig        bool
	version              bool
}

func newApp() *appT {
	return &appT{}
}

func (a *appT) usage(w io.Writer) {
	utils.WriteText(w, usageText)
}

func (a *appT) initFlags(fs *flag.FlagSet) {
	fs.StringVar(&a.configDir, "config", "", "path to alternative golxmd config directory")
	fs.StringVar(&a.rnsConfigDir, "rnsconfig", "", "path to alternative Reticulum config directory")
	fs.BoolVar(&a.runAsPropagationNode, "p", false, "run an LXMF Propagation Node")
	fs.BoolVar(&a.runAsPropagationNode, "propagation-node", false, "run an LXMF Propagation Node")
	fs.StringVar(&a.cmdOnInbound, "i", "", "executable to run when a message is received")
	fs.StringVar(&a.cmdOnInbound, "on-inbound", "", "executable to run when a message is received")
	fs.BoolVar(&a.runAsService, "s", false, "golxmd is running as a service and should log to file")
	fs.BoolVar(&a.runAsService, "service", false, "golxmd is running as a service and should log to file")
	fs.BoolVar(&a.displayStatus, "status", false, "display node status")
	fs.BoolVar(&a.displayPeers, "peers", false, "display peered nodes")
	fs.StringVar(&a.syncHash, "sync", "", "request a sync with the specified peer")
	fs.StringVar(&a.unpeerHash, "b", "", "break peering with the specified peer")
	fs.StringVar(&a.unpeerHash, "break", "", "break peering with the specified peer")
	fs.Var((*timeoutFlag)(&a.timeout), "timeout", "timeout in seconds for query operations")
	fs.StringVar(&a.remoteHash, "r", "", "remote propagation node destination hash")
	fs.StringVar(&a.remoteHash, "remote", "", "remote propagation node destination hash")
	fs.StringVar(&a.identityPath, "identity", "", "path to identity used for remote requests (default: ~/.reticulum/identities/lxmd)")
	fs.BoolVar(&a.exampleConfig, "exampleconfig", false, "print verbose configuration example to stdout and exit")
	fs.BoolVar(&a.version, "version", false, "show program's version number and exit")

	fs.Var(&a.verbosity, "v", "enable verbose logging (stackable)")
	fs.Var(&a.verbosity, "verbose", "enable verbose logging (stackable)")
	fs.Var(&a.quietness, "q", "reduce log verbosity (stackable)")
	fs.Var(&a.quietness, "quiet", "reduce log verbosity (stackable)")
}

func parseFlags(args []string, usageOutput io.Writer) (*appT, error) {
	app := newApp()
	fs := flag.NewFlagSet("golxmd", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		app.usage(usageOutput)
	}
	app.initFlags(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, utils.ErrHelp
		}
		return nil, err
	}
	return app, nil
}

type timeoutFlag time.Duration

func (t *timeoutFlag) String() string {
	return fmt.Sprint(float64(time.Duration(*t).Seconds()))
}

func (t *timeoutFlag) Set(s string) error {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*t = timeoutFlag(time.Duration(f * float64(time.Second)))
	return nil
}

type countFlag int

func (c *countFlag) String() string {
	return fmt.Sprint(int(*c))
}

func (c *countFlag) Set(s string) error {
	if s == "false" {
		return nil
	}
	*c++
	return nil
}

func (c *countFlag) IsBoolFlag() bool {
	return true
}
