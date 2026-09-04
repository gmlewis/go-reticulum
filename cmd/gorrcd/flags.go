// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file parses the gorrcd command-line flags, mirroring Python's
// argparse flag set exactly (cli.py _build_arg_parser).

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/gmlewis/go-reticulum/rrcd"
)

// errHelp is the help sentinel.
var errHelp = errors.New("help requested")

// gorrcdOptions holds the parsed command-line options; the nil pointers
// stand for Python's unset optional values.
type gorrcdOptions struct {
	version          bool
	config           string
	configdir        *string
	identity         string
	roomRegistry     string
	noAnnounce       bool
	announcePeriod   *float64
	hubName          *string
	greeting         *string
	includeJoined    bool
	maxRooms         *int
	maxNickBytes     *int
	maxRoomNameBytes *int
	rateLimit        *int
	maxMsgBodyBytes  *int
	pingInterval     *float64
	pingTimeout      *float64
	logLevel         *string
	logFile          *string
	pprofAddr        string
}

// parseFlags parses the gorrcd argument list; the flag defaults are the
// RRCD_HOME-aware state paths. Help maps to the errHelp sentinel.
func parseFlags(args []string, usageOutput io.Writer) (*gorrcdOptions, error) {
	opts := &gorrcdOptions{
		config:       rrcd.DefaultConfigPath(),
		identity:     rrcd.DefaultIdentityPath(),
		roomRegistry: rrcd.DefaultRoomRegistryPath(),
	}
	fs := flag.NewFlagSet("gorrcd", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { printUsage(usageOutput) }

	fs.BoolVar(&opts.version, "version", false,
		"show program's version number and exit")
	fs.StringVar(&opts.config, "config", opts.config,
		"Path to a TOML config file (created on first run)")
	fs.Func("configdir", "Reticulum config directory", func(v string) error {
		opts.configdir = &v
		return nil
	})
	fs.StringVar(&opts.identity, "identity", opts.identity,
		"Path to hub identity file (created on first run)")
	fs.StringVar(&opts.roomRegistry, "room-registry", opts.roomRegistry,
		"Path to separate room registry TOML (created on first run)")
	fs.BoolVar(&opts.noAnnounce, "no-announce", false,
		"Disable announce on start (does not affect periodic announce)")
	fs.Func("announce-period", "Periodic announce interval seconds (0 disables)", func(v string) error {
		f, err := parsePythonFloat(v)
		if err != nil {
			return err
		}
		opts.announcePeriod = &f
		return nil
	})
	fs.Func("hub-name", "Hub name in WELCOME", func(v string) error {
		opts.hubName = &v
		return nil
	})
	fs.Func("greeting", "Greeting delivered via NOTICE after WELCOME", func(v string) error {
		opts.greeting = &v
		return nil
	})
	fs.BoolVar(&opts.includeJoined, "include-joined-member-list", false,
		"Include member list in JOINED (best-effort)")
	fs.Func("max-rooms", "Max rooms per session", func(v string) error {
		n, err := parsePythonInt(v)
		if err != nil {
			return err
		}
		opts.maxRooms = &n
		return nil
	})
	fs.Func("max-nick-bytes", "Max nickname size in UTF-8 bytes", func(v string) error {
		n, err := parsePythonInt(v)
		if err != nil {
			return err
		}
		opts.maxNickBytes = &n
		return nil
	})
	fs.Func("max-room-name-bytes", "Max room name size in UTF-8 bytes", func(v string) error {
		n, err := parsePythonInt(v)
		if err != nil {
			return err
		}
		opts.maxRoomNameBytes = &n
		return nil
	})
	fs.Func("rate-limit-msgs-per-minute", "Per-link message rate limit", func(v string) error {
		n, err := parsePythonInt(v)
		if err != nil {
			return err
		}
		opts.rateLimit = &n
		return nil
	})
	fs.Func("max-msg-body-bytes", "Maximum message body size in UTF-8 bytes", func(v string) error {
		n, err := parsePythonInt(v)
		if err != nil {
			return err
		}
		opts.maxMsgBodyBytes = &n
		return nil
	})
	fs.Func("ping-interval", "Hub-initiated PING interval seconds (0 disables)", func(v string) error {
		f, err := parsePythonFloat(v)
		if err != nil {
			return err
		}
		opts.pingInterval = &f
		return nil
	})
	fs.Func("ping-timeout", "Close link if PONG not received within this many seconds (0 disables)", func(v string) error {
		f, err := parsePythonFloat(v)
		if err != nil {
			return err
		}
		opts.pingTimeout = &f
		return nil
	})
	fs.Func("log-level", "Logging level override (DEBUG, INFO, WARNING, ERROR). Default comes from config.", func(v string) error {
		opts.logLevel = &v
		return nil
	})
	fs.Func("log-file", "Log file path override (empty disables file logging). Default comes from config.", func(v string) error {
		opts.logFile = &v
		return nil
	})
	fs.StringVar(&opts.pprofAddr, "pprof-addr", "", "Debug pprof HTTP listen address (empty disables)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, err
	}
	if remaining := fs.Args(); len(remaining) > 0 {
		return nil, errors.New("unrecognized arguments: " + remaining[0])
	}
	return opts, nil
}

// parsePythonFloat parses a float the way Python's float() does:
// surrounding whitespace strips, the optional sign and digits may carry
// single underscores between digits, and inf/infinity/nan parse — while
// trailing garbage like "30abc", hex like "0x10", and illegal underscore
// placements raise the argparse ValueError.
func parsePythonFloat(v string) (float64, error) {
	s := strings.TrimSpace(v)
	s, err := pythonLegalNumericUnderscores(s)
	if err != nil {
		return 0, fmt.Errorf("invalid float value: %q", v)
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "0x") {
		return 0, fmt.Errorf("invalid float value: %q", v)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid float value: %q", v)
	}
	return f, nil
}

// parsePythonInt parses an int the way Python's int() does: surrounding
// whitespace strips, an optional sign, base-10 digits with single
// underscores between digits; anything else raises the argparse
// ValueError.
func parsePythonInt(v string) (int, error) {
	s := strings.TrimSpace(v)
	s, err := pythonLegalNumericUnderscores(s)
	if err != nil {
		return 0, fmt.Errorf("invalid int value: %q", v)
	}
	neg := false
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	if s == "" {
		return 0, fmt.Errorf("invalid int value: %q", v)
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid int value: %q", v)
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid int value: %q", v)
	}
	if neg {
		n = -n
	}
	return n, nil
}

// pythonLegalNumericUnderscores removes the single underscores between
// digits that Python's int()/float() accept; an underscore anywhere else
// is a ValueError.
func pythonLegalNumericUnderscores(s string) (string, error) {
	if !strings.Contains(s, "_") {
		return s, nil
	}
	isDigit := func(r byte) bool { return r >= '0' && r <= '9' }
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if c != '_' {
			b.WriteByte(c)
			continue
		}
		if i == 0 || i == len(s)-1 || !isDigit(s[i-1]) || !isDigit(s[i+1]) {
			return "", fmt.Errorf("illegal underscore")
		}
	}
	return b.String(), nil
}

func printUsage(w io.Writer) {
	if w == nil {
		w = io.Discard
	}
	_, _ = fmt.Fprint(w, usageText)
}

// usageText mirrors the Python argparse help with the go-prefix
// self-reference.
const usageText = `usage: gorrcd [-h] [--version] [--config CONFIG] [--configdir CONFIGDIR]
            [--identity IDENTITY] [--room-registry ROOM_REGISTRY]
            [--no-announce] [--announce-period ANNOUNCE_PERIOD]
            [--hub-name HUB_NAME] [--greeting GREETING]
            [--include-joined-member-list] [--max-rooms MAX_ROOMS]
            [--max-nick-bytes MAX_NICK_BYTES]
            [--max-room-name-bytes MAX_ROOM_NAME_BYTES]
            [--rate-limit-msgs-per-minute RATE_LIMIT_MSGS_PER_MINUTE]
            [--max-msg-body-bytes MAX_MSG_BODY_BYTES]
            [--ping-interval PING_INTERVAL] [--ping-timeout PING_TIMEOUT]
            [--log-level LOG_LEVEL] [--log-file LOG_FILE]

Run an RRC hub daemon

options:
  -h, --help            show this help message and exit
  --version             show program's version number and exit
  --config CONFIG       Path to a TOML config file (created on first run)
  --configdir CONFIGDIR
                        Reticulum config directory
  --identity IDENTITY   Path to hub identity file (created on first run)
  --room-registry ROOM_REGISTRY
                        Path to separate room registry TOML (created on first
                        run)
  --no-announce         Disable announce on start (does not affect periodic
                        announce)
  --announce-period ANNOUNCE_PERIOD
                        Periodic announce interval seconds (0 disables)
  --hub-name HUB_NAME   Hub name in WELCOME
  --greeting GREETING   Greeting delivered via NOTICE after WELCOME
  --include-joined-member-list
                        Include member list in JOINED (best-effort)
  --max-rooms MAX_ROOMS
                        Max rooms per session
  --max-nick-bytes MAX_NICK_BYTES
                        Max nickname size in UTF-8 bytes
  --max-room-name-bytes MAX_ROOM_NAME_BYTES
                        Max room name size in UTF-8 bytes
  --rate-limit-msgs-per-minute RATE_LIMIT_MSGS_PER_MINUTE
                        Per-link message rate limit
  --max-msg-body-bytes MAX_MSG_BODY_BYTES
                        Maximum message body size in UTF-8 bytes
  --ping-interval PING_INTERVAL
                        Hub-initiated PING interval seconds (0 disables)
  --ping-timeout PING_TIMEOUT
                        Close link if PONG not received within this many
                        seconds (0 disables)
  --log-level LOG_LEVEL
                        Logging level override (DEBUG, INFO, WARNING, ERROR).
                        Default comes from config.
  --log-file LOG_FILE   Log file path override (empty disables file logging).
                        Default comes from config.
`
