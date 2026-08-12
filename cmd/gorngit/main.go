// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gorngit is a Reticulum-native git repository node, compatible with the
// Python rngit tool (RNS/Utilities/rngit, shipped with Reticulum 1.4.2).
//
// It serves git repositories over Reticulum links using the rns:// URL scheme
// and the "git" application namespace, and provides subcommands for repository
// creation, release management, work documents, forking, syncing, and mirroring.
//
// The companion git-remote-rns binary (cmd/gogit-remote-rns) implements the git
// remote-helper protocol so standard git clients can clone, fetch, and push over
// Reticulum.
//
// Usage:
//
//	gorngit node [--config DIR] [--rnsconfig DIR] [-p] [-s] [-i]
//	gorngit create [--config DIR] [--rnsconfig DIR] [-i PATH] <repository>
//	gorngit release [--config DIR] [--rnsconfig DIR] [-i PATH] [-s PATH]
//		[-n NAME] [-L] [-o] [<repository> <operation> <target>]
//	gorngit perms [--config DIR] [--rnsconfig DIR] [-i PATH] <remote>
//	gorngit work [--config DIR] [--rnsconfig DIR] [-i PATH] [--scope SCOPE]
//		[-t TITLE] [-d ID] [<repository> <operation>]
//	gorngit fork [--config DIR] [--rnsconfig DIR] [-i PATH] <source> <target>
//	gorngit sync [--config DIR] [--rnsconfig DIR] [-i PATH] <repository>
//	gorngit mirror [--config DIR] [--rnsconfig DIR] [-i PATH] [--scope SCOPE]
//		<source> <target>
package main

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/gmlewis/go-reticulum/rns"
)

// appName is the RNS application namespace used by rngit (RNS/Utilities/rngit/
// __init__.py: APP_NAME = "git").
const appName = "git"

// protoSpec is the URL scheme prefix for rngit remote URLs.
const protoSpec = "rns://"

func main() {
	log.SetFlags(0)
	os.Exit(run(os.Args[1:]))
}

// run dispatches gorngit subcommands. It is the entry point used by tests.
// It mirrors server.main + program_setup (rngit v1.4.2).
func run(args []string) int {
	return runWithOutput(args, os.Stdout, os.Stderr)
}

// runWithOutput is the testable form of run, writing to the provided writers.
func runWithOutput(args []string, stdout, stderr io.Writer) int {
	opts, err := parseFlags(args, stderr)
	if err != nil {
		if err == errHelp {
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}

	if opts.version {
		_, _ = fmt.Fprintf(stdout, "gorngit %s\n", rns.VERSION)
		return 0
	}

	switch opts.subcommand {
	case subNode:
		return runNode(opts, stdout, stderr)
	case subCreate:
		return runCreate(opts, stdout, stderr)
	case subFork:
		return runFork(opts, stdout, stderr)
	case subSync:
		return runSync(opts, stdout, stderr)
	case subMirror:
		return runMirror(opts, stdout, stderr)
	case subRelease:
		return runRelease(opts, stdout, stderr)
	case subWork:
		return runWork(opts, stdout, stderr)
	case subPerms:
		return runPerms(opts, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown subcommand %q\n", opts.subcommand)
		return 1
	}
}

// runNode runs the server serve loop (or prints identity when -p is set),
// mirroring program_setup for the node subcommand (server.py).
func runNode(opts options, stdout, stderr io.Writer) int {
	if !ensureGit() {
		_, _ = fmt.Fprintf(stderr, "The \"git\" command is not available. Aborting server startup.\n")
		return 255
	}

	logger := rns.NewLogger()
	if opts.quiet > 0 {
		logger.SetLogLevel(rns.LogCritical)
	} else if opts.verbose > 0 {
		logger.SetLogLevel(rns.LogVerbose + opts.verbose - 1)
	} else {
		logger.SetLogLevel(rns.LogInfo)
	}

	node, err := newReticulumGitNode(opts.configDir, logger)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Could not initialize git node: %s\n", err)
		return 255
	}

	if opts.printIdentity {
		clientIdentityPath := opts.configDir + "/client_identity"
		clientIdentity, err := loadOrCreateIdentity(clientIdentityPath, logger)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Could not load client identity: %s\n", err)
			return 255
		}
		destHash := rns.CalculateHash(node.identity, appName, repoAspect)
		_, _ = fmt.Fprintf(stdout, "Git Peer Identity         : %s\n", rns.PrettyHex(clientIdentity.Hash))
		_, _ = fmt.Fprintf(stdout, "Repository Node Identity  : %s\n", rns.PrettyHex(node.identity.Hash))
		_, _ = fmt.Fprintf(stdout, "Repositories Destination  : %s\n", rns.PrettyHex(destHash))
		if node.config.serveNomadnet {
			nomadHash := rns.CalculateHash(node.identity, pageAppName, "node")
			_, _ = fmt.Fprintf(stdout, "Nomad Network Destination : %s\n", rns.PrettyHex(nomadHash))
		}
		return 0
	}

	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, opts.rnsConfigDir, logger)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Could not initialize Reticulum: %s\n", err)
		return 255
	}
	defer func() {
		if err := ret.Close(); err != nil {
			logger.Warning("Could not close Reticulum: %v", err)
		}
	}()

	if err := node.serve(ts, logger); err != nil {
		_, _ = fmt.Fprintf(stderr, "Server error: %s\n", err)
		return 255
	}
	return 0
}

// runCreate runs the create subcommand, connecting to a remote node and
// creating a repository, mirroring program_setup for the create subcommand
// (server.py).
func runCreate(opts options, stdout, stderr io.Writer) int {
	if !ensureGit() {
		_, _ = fmt.Fprintf(stderr, "The \"git\" command is not available.\n")
		return 255
	}

	if opts.repository == "" {
		_, _ = fmt.Fprintf(stderr, "No repository URL specified\n")
		return 1
	}

	configDir, err := loadClientConfigDir(opts.configDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)

	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, opts.rnsConfigDir, logger)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Could not initialize Reticulum: %s\n", err)
		return 1
	}
	defer func() {
		if err := ret.Close(); err != nil {
			logger.Warning("Could not close Reticulum: %v", err)
		}
	}()

	client, err := newReticulumGitClient(ts, configDir, opts.identityPath, opts.repository, logger)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}

	if err := client.connect(logger); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}
	defer client.teardown()

	if err := client.create(); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}
	return 0
}

// runFork runs the fork subcommand, connecting to the target node and asking it
// to fork the source repository, mirroring program_setup for the fork
// subcommand (server.py).
func runFork(opts options, stdout, stderr io.Writer) int {
	if !ensureGit() {
		_, _ = fmt.Fprintf(stderr, "The \"git\" command is not available.\n")
		return 255
	}
	if opts.source == "" {
		_, _ = fmt.Fprintf(stderr, "No source specified\n")
		return 1
	}
	if opts.target == "" {
		_, _ = fmt.Fprintf(stderr, "No target specified\n")
		return 1
	}

	client, ret, logger, ok := prepareGitClient(opts, opts.target, stderr)
	if !ok {
		return 1
	}
	defer func() {
		if err := ret.Close(); err != nil {
			logger.Warning("Could not close Reticulum: %v", err)
		}
	}()
	defer client.teardown()

	if err := client.fork(opts.source); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}
	return 0
}

// runMirror runs the mirror subcommand, connecting to the target node and
// asking it to mirror the source repository, mirroring program_setup for the
// mirror subcommand (server.py).
func runMirror(opts options, stdout, stderr io.Writer) int {
	if !ensureGit() {
		_, _ = fmt.Fprintf(stderr, "The \"git\" command is not available.\n")
		return 255
	}
	if opts.source == "" {
		_, _ = fmt.Fprintf(stderr, "No source specified\n")
		return 1
	}
	if opts.target == "" {
		_, _ = fmt.Fprintf(stderr, "No target specified\n")
		return 1
	}

	client, ret, logger, ok := prepareGitClient(opts, opts.target, stderr)
	if !ok {
		return 1
	}
	defer func() {
		if err := ret.Close(); err != nil {
			logger.Warning("Could not close Reticulum: %v", err)
		}
	}()
	defer client.teardown()

	if err := client.mirror(opts.source); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}
	return 0
}

// runSync runs the sync subcommand, connecting to a node and asking it to
// re-fetch the repository from its recorded upstream, mirroring program_setup
// for the sync subcommand (server.py).
func runSync(opts options, stdout, stderr io.Writer) int {
	if !ensureGit() {
		_, _ = fmt.Fprintf(stderr, "The \"git\" command is not available.\n")
		return 255
	}
	if opts.repository == "" {
		_, _ = fmt.Fprintf(stderr, "No repository URL specified\n")
		return 1
	}

	client, ret, logger, ok := prepareGitClient(opts, opts.repository, stderr)
	if !ok {
		return 1
	}
	defer func() {
		if err := ret.Close(); err != nil {
			logger.Warning("Could not close Reticulum: %v", err)
		}
	}()
	defer client.teardown()

	if err := client.sync(); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}
	return 0
}

// prepareGitClient loads the client config dir, initializes a Reticulum
// transport, and connects a reticulumGitClient to remoteURL. It returns the
// client, Reticulum handle, logger, and a success flag. When ok is false the
// error has already been written to stderr. The caller must defer ret.Close
// and client.teardown. It mirrors the shared boilerplate of runCreate
// (main.go).
func prepareGitClient(opts options, remoteURL string, stderr io.Writer) (*reticulumGitClient, *rns.Reticulum, *rns.Logger, bool) {
	configDir, err := loadClientConfigDir(opts.configDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", err)
		return nil, nil, nil, false
	}

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)

	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, opts.rnsConfigDir, logger)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Could not initialize Reticulum: %s\n", err)
		return nil, nil, nil, false
	}

	client, err := newReticulumGitClient(ts, configDir, opts.identityPath, remoteURL, logger)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", err)
		_ = ret.Close()
		return nil, nil, nil, false
	}

	if err := client.connect(logger); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", err)
		_ = ret.Close()
		return nil, nil, nil, false
	}
	return client, ret, logger, true
}
