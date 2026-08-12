// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/gmlewis/go-reticulum/rns"
)

// runRelease runs the release subcommand, dispatching to the client
// release methods based on opts.operation, mirroring program_setup for
// the release subcommand (server.py). Operations: list, view, fetch,
// verify (offline fetch), create, delete, latest.
//
// The -s/--signer flag is overloaded in Python: for "create" it is a
// path to a signing identity file; for "fetch"/"verify" it is the hex
// hash of the required signer identity. This mirrors that quirk.
func runRelease(opts options, stdout, stderr io.Writer) int {
	if !ensureGit() {
		fmt.Fprintf(stderr, "The \"git\" command is not available.\n")
		return 255
	}
	if opts.operation == "" {
		fmt.Fprintf(stderr, "No operation specified\n")
		return 1
	}

	// create and verify may not need a remote connection (create with
	// -L/--local generates locally; verify is offline). All other
	// operations require a remote URL.
	needsRemote := true
	if opts.operation == "create" && opts.local {
		needsRemote = false
	}
	if opts.operation == "verify" {
		needsRemote = false
	}
	if needsRemote && opts.repository == "" {
		fmt.Fprintf(stderr, "No remote specified\n")
		return 1
	}

	switch opts.operation {
	case "list":
		return runReleaseConnected(opts, stderr, func(c *reticulumGitClient) error {
			return c.listReleases()
		})
	case "view":
		if opts.target == "" {
			fmt.Fprintf(stderr, "No target specified\n")
			return 1
		}
		return runReleaseConnected(opts, stderr, func(c *reticulumGitClient) error {
			return c.viewRelease(opts.target)
		})
	case "fetch":
		// For fetch, --signer is the hex hash of the required signer.
		return runReleaseConnected(opts, stderr, func(c *reticulumGitClient) error {
			return c.fetchRelease(opts.target, opts.signer, false)
		})
	case "verify":
		return runReleaseOffline(opts, stderr)
	case "create":
		return runReleaseCreate(opts, stderr)
	case "delete":
		if opts.target == "" {
			fmt.Fprintf(stderr, "No target specified\n")
			return 1
		}
		return runReleaseConnected(opts, stderr, func(c *reticulumGitClient) error {
			return c.deleteRelease(opts.target)
		})
	case "latest":
		if opts.target == "" {
			fmt.Fprintf(stderr, "No target specified\n")
			return 1
		}
		return runReleaseConnected(opts, stderr, func(c *reticulumGitClient) error {
			return c.latestRelease(opts.target)
		})
	default:
		fmt.Fprintf(stderr, "Unknown release operation %q\n", opts.operation)
		return 1
	}
}

// loadSignerIdentity loads a signing identity from the path in
// opts.signer (for the create operation). Returns nil when no signer
// path is set. The caller falls back to the client identity when nil.
func loadSignerIdentity(opts options) *rns.Identity {
	if opts.signer == "" {
		return nil
	}
	signerPath := expandUser(opts.signer)
	if _, err := os.Stat(signerPath); err != nil {
		return nil
	}
	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	id, err := rns.FromFile(signerPath, logger)
	if err != nil || id == nil {
		return nil
	}
	return id
}

// runReleaseConnected connects to the remote and runs fn with the
// connected client. It mirrors the connect_remote boilerplate of the
// Python release client methods.
func runReleaseConnected(opts options, stderr io.Writer, fn func(*reticulumGitClient) error) int {
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
	if err := fn(client); err != nil {
		if err == errOfflineOK {
			return 0
		}
		fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}
	return 0
}

// runReleaseOffline runs the offline verify path (no connection), which
// validates a local .rsm manifest. For verify, --signer is the hex hash
// of the required signer.
func runReleaseOffline(opts options, stderr io.Writer) int {
	if opts.repository == "" {
		fmt.Fprintf(stderr, "No remote specified\n")
		return 1
	}
	client, ret, logger, ok := prepareGitClientOffline(opts, stderr)
	if !ok {
		return 1
	}
	defer func() {
		if err := ret.Close(); err != nil {
			logger.Warning("Could not close Reticulum: %v", err)
		}
	}()
	if err := client.fetchRelease(opts.target, opts.signer, true); err != nil {
		if err == errOfflineOK {
			return 0
		}
		fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}
	return 0
}

// runReleaseCreate runs the create operation, which may be local-only
// (with -L) or uploaded. For create, --signer is a path to a signing
// identity file; the client identity is the fallback signer.
func runReleaseCreate(opts options, stderr io.Writer) int {
	if opts.target == "" {
		fmt.Fprintf(stderr, "No target specified\n")
		return 1
	}
	if opts.repository == "" {
		fmt.Fprintf(stderr, "No remote specified\n")
		return 1
	}

	signer := loadSignerIdentity(opts)

	if opts.local {
		client, ret, logger, ok := prepareGitClientOffline(opts, stderr)
		if !ok {
			return 1
		}
		defer func() {
			if err := ret.Close(); err != nil {
				logger.Warning("Could not close Reticulum: %v", err)
			}
		}()
		if signer == nil {
			signer = client.identity
		}
		if err := client.createRelease(opts.target, signer, opts.name, true); err != nil {
			fmt.Fprintf(stderr, "%s\n", err)
			return 1
		}
		return 0
	}

	return runReleaseConnected(opts, stderr, func(c *reticulumGitClient) error {
		if signer == nil {
			signer = c.identity
		}
		return c.createRelease(opts.target, signer, opts.name, false)
	})
}

// prepareGitClientOffline creates a client (and Reticulum transport for
// identity loading) without connecting a link. The client's
// destinationHash / repoPath are parsed from remoteURL. Used by offline
// verify and local-only create.
func prepareGitClientOffline(opts options, stderr io.Writer) (*reticulumGitClient, *rns.Reticulum, *rns.Logger, bool) {
	configDir, err := loadClientConfigDir(opts.configDir)
	if err != nil {
		fmt.Fprintf(stderr, "%s\n", err)
		return nil, nil, nil, false
	}
	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, opts.rnsConfigDir, logger)
	if err != nil {
		fmt.Fprintf(stderr, "Could not initialize Reticulum: %s\n", err)
		return nil, nil, nil, false
	}
	client, err := newReticulumGitClient(ts, configDir, opts.identityPath, opts.repository, logger)
	if err != nil {
		fmt.Fprintf(stderr, "%s\n", err)
		_ = ret.Close()
		return nil, nil, nil, false
	}
	return client, ret, logger, true
}
