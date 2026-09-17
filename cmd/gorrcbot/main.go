// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gorrcbot is a headless, always-on RRC (Reticulum Relay Chat) bot client.
//
// It is a CLIENT, not a hub: it dials every hub named in its configuration
// file, joins that hub's configured rooms, and stays connected across link
// flaps and restarts. It answers only when it is addressed by name, and it
// says nothing at all otherwise.
//
// This executable is a thin wrapper. The engine, the field tools, the sensors,
// and the captive portal all live in the shared library package
// github.com/gmlewis/go-reticulum/bot, which the single-appliance Lifesaver
// executable (cmd/grl) runs in-process; this file only parses the command line
// and hands the result to bot.Run.
//
// It brings up the bot in four steps:
//   - First-run bootstrap: creates ~/.gorrcbot/config.toml and
//     ~/.gorrcbot/bot_identity (GORRCBOT_HOME overrides the directory) and
//     exits 0, so an operator always edits the hubs before anything connects.
//   - Identity: the bot owns one Reticulum identity, used on every hub, so its
//     identity hash is the same everywhere and a peer can address it by a hash
//     prefix without knowing which hub it is on.
//   - Engine: one hub session per configured hub, each with its own reconnects
//     and joins. It introduces itself with one NOTICE per room per session only
//     when announce_on_join = true; by default it arrives and leaves as
//     silently as any other member.
//   - Commands: the reply policy plus the command registry. Nothing is answered
//     unless the message starts with @<nick>, @<hash prefix>, or is a direct
//     NOTICE to the bot.
//
// Usage:
//
//	gorrcbot [--config CONFIG] [--bot-config BOT_CONFIG]
//	        [--identity IDENTITY] [--home HOME] [--nick NICK]
//	        [--check-config] [--log-level LEVEL] [--log-file PATH]
//	        [--pprof-addr ADDR] [--version]
//
// State paths honor the GORRCBOT_HOME environment variable (used literally when
// truthy, no expansion) with ~/.gorrcbot as the default home.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gmlewis/go-reticulum/bot"
	"github.com/gmlewis/go-reticulum/rns"
)

// Exit codes. A clean exit is 0, an operational failure is 1, and a command
// line that cannot mean anything is 2, which is what every other tool in this
// repository does.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	log.SetFlags(0)
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run parses the command line, applies it to the shared engine, and reports the
// exit code. It is separate from main so every deferred teardown runs before the
// process ends.
func run(args []string, usageOutput io.Writer) int {
	opts, err := parseFlags(args, usageOutput)
	if err != nil {
		if errors.Is(err, errHelp) {
			return exitOK
		}
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}

	if opts.version {
		fmt.Printf("gorrcbot %v\n", rns.VERSION)
		return exitOK
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err = bot.Run(ctx, bot.Options{
		ConfigDir:   opts.configDir,
		BotConfig:   opts.botConfig,
		Identity:    opts.identity,
		Home:        opts.home,
		Nick:        opts.nick,
		LogLevel:    opts.logLevel,
		LogFile:     opts.logFile,
		PProfAddr:   opts.pprofAddr,
		CheckConfig: opts.checkConfig,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
	})
	// Creating the first-run files is a successful exit: the operator has
	// editing to do before the bot may connect to anything.
	if errors.Is(err, bot.ErrFirstRun) {
		return exitOK
	}
	if err != nil {
		log.Printf("gorrcbot: %v", err)
		return exitFailure
	}
	return exitOK
}
