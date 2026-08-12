// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
)

// runWork runs the work subcommand, dispatching to the client work methods
// based on opts.operation, mirroring program_setup for the work subcommand
// (server.py). Operations: list, view, create, propose, edit, delete,
// update (→comment), complete, activate, perms.
func runWork(opts options, stdout, stderr io.Writer) int {
	if !ensureGit() {
		_, _ = fmt.Fprintf(stderr, "The \"git\" command is not available.\n")
		return 255
	}
	if opts.operation == "" {
		_, _ = fmt.Fprintf(stderr, "No operation specified\n")
		return 1
	}
	if opts.repository == "" {
		_, _ = fmt.Fprintf(stderr, "No remote specified\n")
		return 1
	}

	switch opts.operation {
	case "list":
		return runWorkConnected(opts, stderr, func(c *reticulumGitClient) error {
			return c.workList(opts.scope)
		})
	case "view":
		if opts.docID == 0 {
			_, _ = fmt.Fprintf(stderr, "No document ID specified\n")
			return 1
		}
		return runWorkConnected(opts, stderr, func(c *reticulumGitClient) error {
			return c.workView(opts.docID, opts.scope)
		})
	case "create":
		if opts.title == "" {
			_, _ = fmt.Fprintf(stderr, "No title specified\n")
			return 1
		}
		return runWorkConnected(opts, stderr, func(c *reticulumGitClient) error {
			return c.workCreate(opts.title)
		})
	case "propose":
		if opts.title == "" {
			_, _ = fmt.Fprintf(stderr, "No title specified\n")
			return 1
		}
		return runWorkConnected(opts, stderr, func(c *reticulumGitClient) error {
			return c.workPropose(opts.title)
		})
	case "edit":
		if opts.docID == 0 {
			_, _ = fmt.Fprintf(stderr, "No document ID specified\n")
			return 1
		}
		return runWorkConnected(opts, stderr, func(c *reticulumGitClient) error {
			return c.workEdit(opts.docID, opts.title, opts.scope)
		})
	case "delete":
		if opts.docID == 0 {
			_, _ = fmt.Fprintf(stderr, "No document ID specified\n")
			return 1
		}
		return runWorkConnected(opts, stderr, func(c *reticulumGitClient) error {
			return c.workDelete(opts.docID, opts.scope)
		})
	case "update":
		if opts.docID == 0 {
			_, _ = fmt.Fprintf(stderr, "No document ID specified\n")
			return 1
		}
		return runWorkConnected(opts, stderr, func(c *reticulumGitClient) error {
			return c.workComment(opts.docID, opts.scope)
		})
	case "complete":
		if opts.docID == 0 {
			_, _ = fmt.Fprintf(stderr, "No document ID specified\n")
			return 1
		}
		return runWorkConnected(opts, stderr, func(c *reticulumGitClient) error {
			return c.workComplete(opts.docID)
		})
	case "activate":
		if opts.docID == 0 {
			_, _ = fmt.Fprintf(stderr, "No document ID specified\n")
			return 1
		}
		return runWorkConnected(opts, stderr, func(c *reticulumGitClient) error {
			return c.workActivate(opts.docID)
		})
	case "perms":
		if opts.docID == 0 {
			_, _ = fmt.Fprintf(stderr, "No document ID specified\n")
			return 1
		}
		return runWorkConnected(opts, stderr, func(c *reticulumGitClient) error {
			return c.workPermissions(opts.docID)
		})
	default:
		_, _ = fmt.Fprintf(stderr, "Unknown work operation %q\n", opts.operation)
		return 1
	}
}

// runWorkConnected connects to the remote and runs fn with the connected
// client, mirroring the connect_remote boilerplate of the Python work
// client methods. It reuses the shared prepareGitClient helper.
func runWorkConnected(opts options, stderr io.Writer, fn func(*reticulumGitClient) error) int {
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
		_, _ = fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}
	return 0
}
