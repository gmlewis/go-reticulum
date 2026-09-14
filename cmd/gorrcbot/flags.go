// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/gmlewis/go-reticulum/utils"
)

// errHelp reports that the flags asked for the usage text.
var errHelp = errors.New("help requested")

// botOptions is the command line, after parsing.
type botOptions struct {
	// version asks for the version string and nothing else.
	version bool
	// checkConfig loads and validates the configuration, prints what it found,
	// and exits without connecting. It is the way to debug a configuration
	// file without waiting on a hub.
	checkConfig bool
	// configDir is the Reticulum configuration directory. Empty means the
	// Reticulum default.
	configDir string
	// botConfig overrides the path of the bot's own TOML file.
	botConfig string
	// identity overrides the path of the bot's identity file.
	identity string
	// home overrides the gorrbot home directory, which defaults to
	// GORRCBOT_HOME or ~/.gorrcbot.
	home string
	// nick overrides the advertised nick for every hub.
	nick string
	// logLevel and logFile configure the RNS logger.
	logLevel string
	logFile  string
	// pprofAddr turns on the debug HTTP server when it is not empty.
	pprofAddr string
	// args holds any positional arguments, which gorrbot does not accept.
	args []string
}

// usage writes the usage text.
func (o *botOptions) usage(w io.Writer) {
	utils.WriteText(w, usageText)
}

// parseFlags parses the command line into options.
func parseFlags(args []string, usageOutput io.Writer) (*botOptions, error) {
	opts := &botOptions{}
	fs := flag.NewFlagSet("gorrcbot", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { opts.usage(usageOutput) }

	fs.BoolVar(&opts.version, "version", false, "show program's version number and exit")
	fs.BoolVar(&opts.version, "V", false, "show program's version number and exit")
	fs.BoolVar(&opts.checkConfig, "check-config", false,
		"load and validate the configuration, print a summary, and exit")
	fs.StringVar(&opts.configDir, "config", "",
		"path to an alternative Reticulum config directory")
	fs.StringVar(&opts.botConfig, "bot-config", "",
		"path to the bot's TOML configuration file")
	fs.StringVar(&opts.identity, "identity", "",
		"path to the bot's identity file")
	fs.StringVar(&opts.home, "home", "",
		"gorrbot home directory (default GORRCBOT_HOME or ~/.gorrcbot)")
	fs.StringVar(&opts.nick, "nick", "",
		"override the advertised nick on every hub")
	fs.StringVar(&opts.logLevel, "log-level", "",
		"logging level override (DEBUG, INFO, WARNING, ERROR)")
	fs.StringVar(&opts.logFile, "log-file", "",
		"log file path override (empty disables file logging)")
	fs.StringVar(&opts.pprofAddr, "pprof-addr", "",
		"debug pprof HTTP listen address (empty disables)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, err
	}
	opts.args = append([]string{}, fs.Args()...)
	if err := opts.validate(); err != nil {
		return nil, err
	}
	return opts, nil
}

// validate rejects a command line that cannot mean anything.
func (o *botOptions) validate() error {
	if len(o.args) > 0 {
		return errors.New("unrecognized arguments: " + strings.Join(o.args, " "))
	}
	return nil
}

// usageText is the help text, in the shape the other gorrbot tools use.
const usageText = `usage: gorrbot [-h] [--version] [--check-config] [--config CONFIG]
               [--bot-config BOT_CONFIG] [--identity IDENTITY] [--home HOME]
               [--nick NICK] [--log-level LOG_LEVEL] [--log-file LOG_FILE]
               [--pprof-addr PPROF_ADDR]

Run a headless RRC (Reticulum Relay Chat) bot client

gorrbot connects to every hub in its configuration file at once, joins the
configured rooms on each, and answers only when it is addressed by name. It
keeps its own Reticulum identity so every hub sees the same identity hash.

options:
  -h, --help            show this help message and exit
  --version             show program's version number and exit
  --check-config        load and validate the configuration, print a summary,
                        and exit
  --config CONFIG       path to an alternative Reticulum config directory
  --bot-config BOT_CONFIG
                        path to the bot's TOML configuration file
  --identity IDENTITY   path to the bot's identity file
  --home HOME           gorrbot home directory (default GORRCBOT_HOME or
                        ~/.gorrcbot)
  --nick NICK           override the advertised nick on every hub
  --log-level LOG_LEVEL
                        logging level override (DEBUG, INFO, WARNING, ERROR)
  --log-file LOG_FILE   log file path override (empty disables file logging)
  --pprof-addr PPROF_ADDR
                        debug pprof HTTP listen address (empty disables)

files:
  CONFIG        ~/.gorrcbot/config.toml   hubs, rooms, address policy
  IDENTITY      ~/.gorrcbot/bot_identity  the bot's Reticulum identity

The configuration file and the identity are created on the first run, which
then exits so the operator can edit the hubs before the bot connects.
`
