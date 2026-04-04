// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type options struct {
	configDir     string
	identityPath  string
	serviceName   string
	printIdentity bool
	listen        bool
	verbose       bool
	quiet         bool
	version       bool
	destination   string
	announceEvery int
	noAuth        bool
	allowHashes   []string
	remoteAsArgs  bool
	noRemoteCmd   bool
	noID          bool
	noTTY         bool
	mirror        bool
	timeoutSec    int
	commandLine   []string
}

type multiValueFlag []string

func (m *multiValueFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiValueFlag) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	*m = append(*m, value)
	return nil
}

func usage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage:")
	_, _ = fmt.Fprintln(w, "  gornsh -l [-c <configdir>] [-i <identityfile> | -s <service_name>] [-v | -q] -p")
	_, _ = fmt.Fprintln(w, "  gornsh [-c <configdir>] [-i <identityfile>] [-v | -q] -p")
	_, _ = fmt.Fprintln(w, "  gornsh [-c <configdir>] [-i <identityfile>] [-v | -q] <destination_hash> [--] [program [args ...]]")
	_, _ = fmt.Fprintln(w, "")
}

func parseFlags(args []string) (options, error) {
	var opts options

	fs := flag.NewFlagSet("gornsh", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		usage(io.Discard)
	}

	configShort := fs.String("c", "", "alternate Reticulum config directory")
	configLong := fs.String("config", "", "alternate Reticulum config directory")
	identityShort := fs.String("i", "", "specific identity file to use")
	identityLong := fs.String("identity", "", "specific identity file to use")
	serviceShort := fs.String("s", "", "service name for identity file when listening")
	serviceLong := fs.String("service", "", "service name for identity file when listening")
	printIdentityShort := fs.Bool("p", false, "print identity information and exit")
	printIdentityLong := fs.Bool("print-identity", false, "print identity information and exit")
	listenShort := fs.Bool("l", false, "listen (server) mode")
	listenLong := fs.Bool("listen", false, "listen (server) mode")
	verboseShort := fs.Bool("v", false, "increase verbosity")
	verboseLong := fs.Bool("verbose", false, "increase verbosity")
	quietShort := fs.Bool("q", false, "increase quietness")
	quietLong := fs.Bool("quiet", false, "increase quietness")
	noIDShort := fs.Bool("N", false, "disable identify on connect")
	noIDLong := fs.Bool("no-id", false, "disable identify on connect")
	noTTYShort := fs.Bool("T", false, "force pipe mode (no TTY)")
	noTTYLong := fs.Bool("no-tty", false, "force pipe mode (no TTY)")
	mirrorShort := fs.Bool("m", false, "return with code of remote process")
	mirrorLong := fs.Bool("mirror", false, "return with code of remote process")
	timeoutShort := fs.String("w", "", "client connect/request timeout in seconds")
	timeoutLong := fs.String("timeout", "", "client connect/request timeout in seconds")
	announceShort := fs.String("b", "", "announce on startup and every PERIOD seconds (0 for once)")
	announceLong := fs.String("announce", "", "announce on startup and every PERIOD seconds (0 for once)")
	noAuthShort := fs.Bool("n", false, "disable authentication")
	noAuthLong := fs.Bool("no-auth", false, "disable authentication")
	remoteAsArgsShort := fs.Bool("A", false, "append remote command as args to listener command")
	remoteAsArgsLong := fs.Bool("remote-command-as-args", false, "append remote command as args to listener command")
	noRemoteCmdShort := fs.Bool("C", false, "disable executing remote command")
	noRemoteCmdLong := fs.Bool("no-remote-command", false, "disable executing remote command")
	version := fs.Bool("version", false, "show version")
	var allowValues multiValueFlag
	fs.Var(&allowValues, "a", "identity hash allowed to connect")
	fs.Var(&allowValues, "allowed", "identity hash allowed to connect")

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}

	opts.configDir = firstNonEmpty(*configShort, *configLong)
	opts.identityPath = firstNonEmpty(*identityShort, *identityLong)
	opts.serviceName = firstNonEmpty(*serviceShort, *serviceLong)
	opts.printIdentity = *printIdentityShort || *printIdentityLong
	opts.listen = *listenShort || *listenLong
	opts.verbose = *verboseShort || *verboseLong
	opts.quiet = *quietShort || *quietLong
	opts.version = *version
	opts.noAuth = *noAuthShort || *noAuthLong
	opts.remoteAsArgs = *remoteAsArgsShort || *remoteAsArgsLong
	opts.noRemoteCmd = *noRemoteCmdShort || *noRemoteCmdLong
	opts.noID = *noIDShort || *noIDLong
	opts.noTTY = *noTTYShort || *noTTYLong
	opts.mirror = *mirrorShort || *mirrorLong
	opts.allowHashes = append(opts.allowHashes, allowValues...)
	opts.timeoutSec = 15

	timeoutText := firstNonEmpty(*timeoutShort, *timeoutLong)
	if timeoutText != "" {
		if timeout, err := strconv.Atoi(timeoutText); err == nil && timeout > 0 {
			opts.timeoutSec = timeout
		}
	}

	announceText := firstNonEmpty(*announceShort, *announceLong)
	if announceText != "" {
		if announceEvery, err := strconv.Atoi(announceText); err == nil {
			opts.announceEvery = announceEvery
		}
	}

	if opts.listen && opts.serviceName == "" {
		opts.serviceName = defaultServiceName
	}
	opts.serviceName = sanitizeServiceName(opts.serviceName)

	if fs.NArg() > 0 {
		if opts.listen {
			opts.commandLine = append([]string{}, fs.Args()...)
		} else {
			opts.destination = fs.Arg(0)
			if fs.NArg() > 1 {
				opts.commandLine = append([]string{}, fs.Args()[1:]...)
			}
		}
	}

	return opts, nil
}
