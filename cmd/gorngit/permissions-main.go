// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
)

// runPerms runs the perms subcommand, dispatching to groupPermissions or
// repositoryPermissions based on the remote URL depth, mirroring
// program_setup for the perms subcommand (server.py): 4 URL components
// (rns://<hash>/<group>) → gperms; 5 components
// (rns://<hash>/<group>/<repo>) → rperms; otherwise invalid.
func runPerms(opts options, stdout, stderr io.Writer) int {
	remote := strings.TrimRight(opts.remote, "/")
	if remote == "" {
		fmt.Fprintf(stderr, "No remote specified\n")
		return 1
	}
	components := len(strings.Split(remote, "/"))
	switch components {
	case 5:
		return runPermsConnected(opts, remote, stderr, false, func(c *reticulumGitClient) error {
			return c.repositoryPermissions()
		})
	case 4:
		return runPermsConnected(opts, remote, stderr, true, func(c *reticulumGitClient) error {
			return c.groupPermissions()
		})
	default:
		fmt.Fprintf(stderr, "Invalid URL\n")
		return 1
	}
}

// runPermsConnected connects to the remote and runs fn with the connected
// client, mirroring the connect_remote boilerplate of the Python
// group_permissions / repository_permissions methods. When groupClient is
// true, the remote is a two-component group URL and a group client is
// constructed via newReticulumGitGroupClient.
func runPermsConnected(opts options, remoteURL string, stderr io.Writer, groupClient bool, fn func(*reticulumGitClient) error) int {
	configDir, err := loadClientConfigDir(opts.configDir)
	if err != nil {
		fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)

	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, opts.rnsConfigDir, logger)
	if err != nil {
		fmt.Fprintf(stderr, "Could not initialize Reticulum: %s\n", err)
		return 1
	}

	var client *reticulumGitClient
	if groupClient {
		client, err = newReticulumGitGroupClient(ts, configDir, opts.identityPath, remoteURL, logger)
	} else {
		client, err = newReticulumGitClient(ts, configDir, opts.identityPath, remoteURL, logger)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s\n", err)
		_ = ret.Close()
		return 1
	}

	if err := client.connect(logger); err != nil {
		fmt.Fprintf(stderr, "%s\n", err)
		_ = ret.Close()
		return 1
	}
	defer func() {
		if err := ret.Close(); err != nil {
			logger.Warning("Could not close Reticulum: %v", err)
		}
	}()
	defer client.teardown()
	if err := fn(client); err != nil {
		fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}
	return 0
}
