// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gobot asks a live RRC bot one question and prints its answer.
//
// It is the single-shot command-line counterpart to joining an RRC hub and
// typing "/msg gobot <command>": it brings up Reticulum with the identity
// already present in the Reticulum configuration directory, connects to the
// hub as an ordinary client, joins the bot's room so the bot can see this
// identity, and sends one private command through the hub's own target
// resolution. Every direct notice the bot sends back is printed to stdout, one
// line per notice, and the tool then exits.
//
// Nothing is created and nothing is left behind: the identity is loaded from
// disk (never generated), the connection is disconnected, and the scratch
// history directory is removed on the way out. No state is written anywhere.
//
// Usage:
//
//	gobot [-dest DEST] [--to NICK] [--room ROOM] [--nick NICK]
//	      [--config CONFIG] [--identity IDENTITY]
//	      [--timeout TIMEOUT] [--quiet QUIET] [--verbose]
//	      COMMAND [ARGUMENT ...]
//
// Examples:
//
//	gobot help
//	gobot help buoy
//	gobot wx Denver
//
// Exit status is 0 when a reply was printed, 1 for an operational failure
// (no identity, hub unreachable, request rejected), 2 for a usage error, and 3
// when the request was delivered but the bot stayed silent.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

// Exit codes. They are named so main never has a bare integer in it, and so the
// "the bot stayed silent" case is distinguishable from a transport failure.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
	exitNoReply = 3
)

func main() {
	log.SetFlags(0)

	opts, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, errHelp) {
			os.Exit(exitOK)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitUsage)
	}
	if opts.version {
		fmt.Printf("gobot %v\n", rns.VERSION)
		os.Exit(exitOK)
	}

	if err := run(context.Background(), opts, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "gobot: %v\n", err)
		if errors.Is(err, ErrNoReply) {
			os.Exit(exitNoReply)
		}
		os.Exit(exitFailure)
	}
}

// run performs one whole exchange and writes the reply lines to stdout. Every
// side effect it opens is closed on the way out, in the reverse order it was
// opened, so the Reticulum instance outlives the RRC manager that rides it.
func run(ctx context.Context, opts *options, stdout, stderr io.Writer) error {
	// Part of the RRC client and the transport log through the standard logger,
	// which writes straight to stderr and cannot be filtered. A quiet run
	// discards it, so an interactive terminal shows the bot's answer and
	// nothing else; --verbose is what asks to see it.
	if opts.verbose {
		log.SetOutput(stderr)
	} else {
		log.SetOutput(io.Discard)
	}

	configDir, err := reticulumConfigDir(opts.configDir)
	if err != nil {
		return err
	}
	identity, identityPath, err := loadIdentity(configDir, opts.identity)
	if err != nil {
		return err
	}
	if opts.verbose {
		note(stderr, "gobot: identity %v from %v\n", hexString(identity.Hash), identityPath)
	}

	hubHash, err := destinationHashBytes(opts.dest)
	if err != nil {
		return err
	}

	// The run's whole deadline covers connecting, joining and waiting for the
	// reply, which is what the operator asked --timeout for.
	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()

	// The Reticulum protocol log is silenced before the instance is built, so
	// nothing can reach stdout, and it is pinned again afterwards because the
	// Reticulum configuration file carries its own loglevel that would
	// otherwise re-enable it.
	logger := baseLogger()
	ret, err := rns.NewReticulumWithLogger(rns.NewTransportSystem(logger), configDir, logger)
	if err != nil {
		return fmt.Errorf("could not start Reticulum: %w", err)
	}
	configureLogger(logger, opts, stderr)
	defer func() {
		if err := ret.Close(); err != nil && opts.verbose {
			note(stderr, "gobot: closing Reticulum: %v\n", err)
		}
	}()

	// The RRC manager persists per-room history this tool never reads back, so
	// it gets a scratch directory that is removed with the run.
	storageDir, err := os.MkdirTemp("", "gobot-")
	if err != nil {
		return fmt.Errorf("could not create a scratch storage directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(storageDir); err != nil && opts.verbose {
			note(stderr, "gobot: removing %v: %v\n", storageDir, err)
		}
	}()

	mgr := rrc.NewManager(storageDir, func() []byte { return identity.Hash })
	mgr.SetIdentity(identity)
	mgr.SetNickname(opts.nick)
	mgr.SetTransport(ret.Transport())
	defer mgr.Shutdown()

	hub := mgr.AddHub(hubHash, rrc.HubDestName, "RRC Hub")
	if hub == nil {
		return errors.New("could not create the hub connection")
	}

	if opts.verbose {
		note(stderr, "gobot: asking @%v on hub %v: %v\n", opts.target, opts.dest, opts.message)
	}
	lines, err := runRequest(ctx, hub, opts, identity.Hash)
	if err != nil {
		return err
	}
	return writeReply(stdout, lines)
}

// writeReply prints the bot's answer, one line per notice, and reports a write
// failure so a closed stdout is surfaced instead of silently truncating the
// reply.
func writeReply(w io.Writer, lines []string) error {
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return fmt.Errorf("could not write the reply: %w", err)
		}
	}
	return nil
}

// note writes one diagnostic line and ignores a write failure: a diagnostic
// that cannot reach its stream has nowhere left to report itself, and failing
// the run over it would hide the answer the operator asked for.
func note(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// baseLogger builds the Reticulum logger in its quiet state. The quiet level is
// set before the Reticulum instance reads its configuration, so nothing the
// instance logs while it starts can land on stdout.
func baseLogger() *rns.Logger {
	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogNone)
	return logger
}

// configureLogger applies this tool's logger policy after the Reticulum instance
// has read the configuration, whose loglevel would otherwise re-enable the
// protocol log. Without --verbose the logger stays silent, so protocol chatter
// can never mix with the bot's reply on stdout; with it, the chatter goes to
// stderr through the callback sink, which keeps stdout for the answer alone.
func configureLogger(logger *rns.Logger, opts *options, stderr io.Writer) {
	if !opts.verbose {
		logger.SetLogLevel(rns.LogNone)
		return
	}
	logger.SetLogDest(rns.LogCallback)
	logger.SetLogCallback(func(line string) { note(stderr, "%v\n", line) })
	logger.SetLogLevel(rns.LogInfo)
}
