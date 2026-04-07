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
	"strings"
)

var errHelp = errors.New("help requested")

type options struct {
	configDir     string
	identityPath  string
	serviceName   string
	printIdentity bool
	listen        bool
	verbose       int
	quiet         int
	version       bool
	destination   string
	announceEvery *int
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

type countFlag struct {
	target *int
}

func (f *countFlag) String() string {
	if f == nil || f.target == nil {
		return "0"
	}
	return fmt.Sprintf("%v", *f.target)
}

func (f *countFlag) Set(string) error {
	if f != nil && f.target != nil {
		*f.target = *f.target + 1
	}
	return nil
}

func (f *countFlag) IsBoolFlag() bool {
	return true
}

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

func splitAtSeparator(args []string, separator string) ([]string, []string, bool) {
	for i, arg := range args {
		if arg == separator {
			return append([]string{}, args[:i]...), append([]string{}, args[i+1:]...), true
		}
	}
	return append([]string{}, args...), nil, false
}

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, usageText)
}

func parseFlags(args []string, usageOutput io.Writer) (options, error) {
	var opts options

	fs := flag.NewFlagSet("gornsh", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		usage(usageOutput)
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
	fs.Var(&countFlag{target: &opts.verbose}, "v", "increase verbosity")
	fs.Var(&countFlag{target: &opts.verbose}, "verbose", "increase verbosity")
	fs.Var(&countFlag{target: &opts.quiet}, "q", "increase quietness")
	fs.Var(&countFlag{target: &opts.quiet}, "quiet", "increase quietness")
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
		if errors.Is(err, flag.ErrHelp) {
			return options{}, errHelp
		}
		return options{}, err
	}

	opts.configDir = firstNonEmpty(*configShort, *configLong)
	opts.identityPath = firstNonEmpty(*identityShort, *identityLong)
	opts.serviceName = firstNonEmpty(*serviceShort, *serviceLong)
	opts.printIdentity = *printIdentityShort || *printIdentityLong
	opts.listen = *listenShort || *listenLong
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
		timeout, err := strconv.Atoi(timeoutText)
		if err != nil {
			return options{}, fmt.Errorf("invalid value for --timeout: %v", timeoutText)
		}
		opts.timeoutSec = timeout
	}

	announceText := firstNonEmpty(*announceShort, *announceLong)
	if announceText != "" {
		announceEvery, err := strconv.Atoi(announceText)
		if err != nil {
			return options{}, fmt.Errorf("invalid value for --announce: %v", announceText)
		}
		opts.announceEvery = &announceEvery
	}

	if opts.listen && opts.serviceName == "" {
		opts.serviceName = defaultServiceName
	}
	opts.serviceName = sanitizeServiceName(opts.serviceName)

	if fs.NArg() > 0 {
		positional, commandLine, foundSeparator := splitAtSeparator(fs.Args(), "--")
		if opts.listen {
			if foundSeparator {
				opts.commandLine = commandLine
			} else {
				opts.commandLine = positional
			}
		} else {
			opts.destination = positional[0]
			if len(positional) > 1 && !foundSeparator {
				opts.commandLine = append([]string{}, positional[1:]...)
			}
			if foundSeparator {
				opts.commandLine = append([]string{}, commandLine...)
			}
		}
	}

	return opts, nil
}

const usageText = `
Usage:
		gornsh -l [-c <configdir>] [-i <identityfile> | -s <service_name>] [-v... | -q...] -p
		gornsh -l [-c <configdir>] [-i <identityfile> | -s <service_name>] [-v... | -q...]
						[-b <period>] [-n] [-a <identity_hash>] ([-a <identity_hash>] ...) [-A | -C]
						[[--] <program> [<arg> ...]]
		gornsh [-c <configdir>] [-i <identityfile>] [-v... | -q...] -p
		gornsh [-c <configdir>] [-i <identityfile>] [-v... | -q...] [-N] [-m] [-w <timeout>] [-T]
				 <destination_hash> [[--] <program> [<arg> ...]]
		gornsh -h
		gornsh --version

Options:
		-c DIR --config DIR          Alternate Reticulum config directory to use
		-i FILE --identity FILE      Specific identity file to use
		-s NAME --service NAME       Service name for identity file if not default
		-p --print-identity          Print identity information and exit
		-l --listen                  Listen (server) mode. If supplied, <program> <arg>...will
																	 be used as the command line when the initiator does not
																	 provide one or when remote command is disabled. If
																	 <program> is not supplied, the default shell of the
																	 user rnsh is running under will be used.
		-b --announce PERIOD         Announce on startup and every PERIOD seconds
																 Specify 0 for PERIOD to announce on startup only.
		-a HASH --allowed HASH       Specify identities allowed to connect. Allowed identities
																	 can also be specified in ~/.rnsh/allowed_identities or
																	 ~/.config/rnsh/allowed_identities, one hash per line.
		-n --no-auth                 Disable authentication
		-N --no-id                   Disable identify on connect
		-A --remote-command-as-args  Concatenate remote command to argument list of <program>/shell
		-C --no-remote-command       Disable executing command line from remote
		-m --mirror                  Client returns with code of remote process
		-w TIME --timeout TIME       Specify client connect and request timeout in seconds
		-T --no-tty                  Force pipe mode (no TTY); useful for ssh ProxyCommand
		-q --quiet                   Increase quietness (move level up), multiple increases effect
																					DEFAULT LOGGING LEVEL
																									CRITICAL (silent)
																		Initiator ->  ERROR
																									WARNING
																		 Listener ->  INFO
																									DEBUG    (insane)
		-v --verbose                 Increase verbosity (move level down), multiple increases effect
		--version                    Show version
		-h --help                    Show this help
`
