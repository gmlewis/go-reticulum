// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"strconv"
	"time"
)

func init() {
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), `
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
`)
	}
	flag.StringVar(&configDir, "config", "", "path to alternative golxmd config directory")
	flag.StringVar(&rnsConfigDir, "rnsconfig", "", "path to alternative Reticulum config directory")
	flag.BoolVar(&runAsPropagationNode, "p", false, "run an LXMF Propagation Node")
	flag.BoolVar(&runAsPropagationNode, "propagation-node", false, "run an LXMF Propagation Node")
	flag.StringVar(&cmdOnInbound, "i", "", "executable to run when a message is received")
	flag.StringVar(&cmdOnInbound, "on-inbound", "", "executable to run when a message is received")
	flag.BoolVar(&runAsService, "s", false, "golxmd is running as a service and should log to file")
	flag.BoolVar(&runAsService, "service", false, "golxmd is running as a service and should log to file")
	flag.BoolVar(&displayStatus, "status", false, "display node status")
	flag.BoolVar(&displayPeers, "peers", false, "display peered nodes")
	flag.StringVar(&syncHash, "sync", "", "request a sync with the specified peer")
	flag.StringVar(&unpeerHash, "b", "", "break peering with the specified peer")
	flag.StringVar(&unpeerHash, "break", "", "break peering with the specified peer")
	flag.Var((*timeoutFlag)(&timeout), "timeout", "timeout in seconds for query operations")
	flag.StringVar(&remoteHash, "r", "", "remote propagation node destination hash")
	flag.StringVar(&remoteHash, "remote", "", "remote propagation node destination hash")
	flag.StringVar(&identityPath, "identity", "", "path to identity used for remote requests (default: ~/.reticulum/identities/lxmd)")
	flag.BoolVar(&exampleConfig, "exampleconfig", false, "print verbose configuration example to stdout and exit")
	flag.BoolVar(&version, "version", false, "show program's version number and exit")

	flag.Var(&verbosity, "v", "enable verbose logging (stackable)")
	flag.Var(&quietness, "q", "reduce log verbosity (stackable)")
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

var (
	// main flags:
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
)
