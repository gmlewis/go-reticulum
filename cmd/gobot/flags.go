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
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
	"github.com/gmlewis/go-reticulum/utils"
)

// errHelp reports that the command line asked for the usage text. main maps it
// onto a clean exit, because asking for help is not an error.
var errHelp = errors.New("help requested")

// Defaults for the command line. The destination is hard-coded to the official
// gonomadnet Public RRC Hub, where the official @gobot runs, so the tool works
// out of the box; the nick, the room and the destination are all overridable
// for anyone who runs their own bot.
const (
	// DefaultDestination is the rrc.hub destination hash of the official
	// gonomadnet Public RRC Hub, in lowercase hexadecimal.
	DefaultDestination = "a012129c10205c0b9441fcd2b755b2a7"
	// DefaultTargetNick is the nick the official bot answers to.
	DefaultTargetNick = "gobot"
	// DefaultRoom is the room the tool joins so the bot learns the sender's
	// identity. A direct reply is only deliverable to a peer the bot has seen
	// in a room it joined.
	DefaultRoom = "general"
	// DefaultOwnNick is the nick this tool advertises in HELLO and JOIN, so the
	// bot's reply and its own logs can name the caller.
	DefaultOwnNick = "gobot-cli"
	// DefaultTimeout bounds the whole run: connecting to the hub, joining the
	// room, and waiting for the first reply line.
	DefaultTimeout = 90 * time.Second
	// DefaultQuietPeriod is how long the reply stream must stay silent before
	// it is considered complete. The bot sends one notice per reply line
	// back-to-back, so a short quiet gap marks the end.
	DefaultQuietPeriod = 2 * time.Second
)

// options is the command line after parsing.
type options struct {
	// dest is the hub's rrc.hub destination hash, 32 hexadecimal characters.
	dest string
	// target is the nick of the bot to address.
	target string
	// room is the room to join so the bot can address the reply.
	room string
	// nick is the caller's own advertised nick.
	nick string
	// configDir is the Reticulum configuration directory, which is also where
	// the identity is looked for.
	configDir string
	// identity is an explicit identity file; empty means the identity found
	// under configDir.
	identity string
	// timeout bounds the whole run.
	timeout time.Duration
	// quiet is the silence that ends the reply stream.
	quiet time.Duration
	// verbose turns the Reticulum protocol log on, on stderr.
	verbose bool
	// version asks for the version string and nothing else.
	version bool
	// message is the command line to send, joined from the positional
	// arguments.
	message string
}

// usage writes the usage text.
func (o *options) usage(w io.Writer) {
	utils.WriteText(w, usageText)
}

// parseFlags parses the command line into options. The positional arguments are
// joined with single spaces into the command line sent to the bot, so
// "gobot help buoy" and "gobot 'help buoy'" mean the same thing.
func parseFlags(args []string, usageOutput io.Writer) (*options, error) {
	opts := &options{}
	fs := flag.NewFlagSet("gobot", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { opts.usage(usageOutput) }

	fs.StringVar(&opts.dest, "dest", DefaultDestination,
		"hub rrc.hub destination hash (32 hex characters)")
	fs.StringVar(&opts.target, "to", DefaultTargetNick,
		"nick of the bot to address")
	fs.StringVar(&opts.room, "room", DefaultRoom,
		"room to join so the bot can answer privately")
	fs.StringVar(&opts.nick, "nick", DefaultOwnNick,
		"nick this tool advertises to the hub")
	fs.StringVar(&opts.configDir, "config", "",
		"Reticulum config directory (default ~/.reticulum)")
	fs.StringVar(&opts.identity, "identity", "",
		"identity file to use (default the identity under the config directory)")
	fs.DurationVar(&opts.timeout, "timeout", DefaultTimeout,
		"overall deadline for connecting, joining and receiving the reply")
	fs.DurationVar(&opts.quiet, "quiet", DefaultQuietPeriod,
		"silence that ends the reply stream")
	fs.BoolVar(&opts.verbose, "verbose", false, "log Reticulum protocol traffic to stderr")
	fs.BoolVar(&opts.version, "version", false, "show program's version number and exit")
	fs.BoolVar(&opts.version, "V", false, "show program's version number and exit")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, err
	}
	opts.message = strings.TrimSpace(strings.Join(fs.Args(), " "))
	if err := opts.validate(); err != nil {
		return nil, err
	}
	return opts, nil
}

// validate rejects a command line that cannot mean anything. The check runs
// after parsing so a usage error names the offending value rather than failing
// later on the wire.
func (o *options) validate() error {
	if o.version {
		return nil
	}
	if _, err := destinationHashBytes(o.dest); err != nil {
		return err
	}
	if strings.TrimSpace(o.target) == "" {
		return errors.New("the target nick must not be empty")
	}
	if strings.TrimSpace(o.room) == "" {
		return errors.New("the room must not be empty")
	}
	if o.timeout <= 0 {
		return errors.New("the timeout must be greater than zero")
	}
	if o.quiet <= 0 {
		return errors.New("the quiet period must be greater than zero")
	}
	if o.message == "" {
		return errors.New("no command given; try \"gobot help\"")
	}
	return nil
}

// destinationHashBytes decodes the hub destination hash, naming the flag on
// failure so a typo is obvious.
func destinationHashBytes(dest string) ([]byte, error) {
	hash, err := rns.HexToBytes(strings.TrimSpace(dest))
	if err != nil {
		return nil, errors.New("invalid -dest destination hash: " + err.Error())
	}
	if len(hash) != rrc.IdentityHashLen {
		return nil, errors.New("invalid -dest destination hash: want 32 hexadecimal characters")
	}
	return hash, nil
}

// usageText is the help text, in the shape the other cmd tools use.
const usageText = `usage: gobot [-h] [--version] [-dest DEST] [--to NICK] [--room ROOM]
             [--nick NICK] [--config CONFIG] [--identity IDENTITY]
             [--timeout TIMEOUT] [--quiet QUIET] [--verbose]
             COMMAND [ARGUMENT ...]

Ask a live RRC bot one question and print its reply

gobot connects to an RRC hub over Reticulum as an ordinary client, joins a room,
and privately asks the bot there the command given on the command line -- the
same exchange as joining the hub and typing "/msg gobot <command>". Every reply
line the bot sends back is printed to stdout, one per line.

The destination is hard-coded to the official gonomadnet Public RRC Hub, home of
the official @gobot; use -dest to talk to a bot on another hub.

The identity is read from the Reticulum configuration directory, and the tool
never creates one: run any Reticulum tool once (for example gornstatus) so an
identity exists, or point --identity at one.

examples:
  gobot help                       the bot's command listing
  gobot help buoy                  help for one command
  gobot wx Denver                  live weather for a place
  gobot loc 849VCWC8+R9            convert a Plus Code to five coordinate forms
  gobot dist CM87uk CM87wj         great-circle distance between two points
  gobot -dest 28c7c1a68c735693aa8e6b8193ed44b2 help

options:
  -h, --help            show this help message and exit
  --version             show program's version number and exit
  -dest DEST            hub rrc.hub destination hash (32 hex characters)
                        (default ` + DefaultDestination + `)
  --to NICK             nick of the bot to address (default ` + DefaultTargetNick + `)
  --room ROOM           room to join so the bot can answer privately
                        (default ` + DefaultRoom + `)
  --nick NICK           nick this tool advertises to the hub
                        (default ` + DefaultOwnNick + `)
  --config CONFIG       Reticulum config directory (default ~/.reticulum)
  --identity IDENTITY   identity file to use (default the identity under the
                        config directory)
  --timeout TIMEOUT     overall deadline for connecting, joining and receiving
                        the reply (default ` + "90s" + `)
  --quiet QUIET         silence that ends the reply stream (default ` + "2s" + `)
  --verbose             log Reticulum protocol traffic to stderr

files:
  IDENTITY    ~/.reticulum/storage/transport_identity   the identity used
`
