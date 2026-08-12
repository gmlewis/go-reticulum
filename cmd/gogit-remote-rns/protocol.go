// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// protocol.go implements the git remote-helper stdin/stdout protocol loop
// for git-remote-rns, mirroring RNS/Utilities/rngit/client.py run() +
// handle_git_list + the per-ref status formatting of process_push_queue.
//
// The RNS request side is abstracted behind helperBackend so the protocol
// state machine is unit-testable without a network. The concrete backend
// (RNS link + /git/list, /git/fetch, /git/push, /git/delete requests) lives
// in client.go.

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// errBadList is returned by a test backend to simulate a server list refusal.
var errBadList = errors.New("bad list")

// fetchRef is a single (sha, ref) pair queued from a "fetch <sha> <ref>" line.
type fetchRef struct {
	sha string
	ref string
}

// pushRef is a single refspec queued from a "push <local>:<remote>" line.
// A pushRef with an empty localRef requests deletion of remoteRef. A leading
// '+' on the local ref (stripped before dispatch) marks a force push.
type pushRef struct {
	localRef  string
	remoteRef string
	force     bool
	deletion  bool
}

// pushStatus is the per-ref outcome of a push, written as "ok <ref>" or
// "error <ref> <escaped-msg>" to git's stdout.
type pushStatus struct {
	remoteRef string
	ok        bool
	msg       string
}

// helperBackend abstracts the RNS request side of the remote helper.
type helperBackend interface {
	// list returns the ref-list body from /git/list (one "<sha> <ref>" line
	// per ref, plus the "@<head> HEAD" symref line). forPush selects the
	// push variant.
	list(forPush bool) (string, error)
	// fetch fetches the requested refs and unbundles them locally. It writes
	// no per-ref status; a non-nil error aborts the helper.
	fetch(refs []fetchRef) error
	// push pushes the requested refs and returns one status per ref. A
	// pushRef with deletion set requests removal of remoteRef.
	push(refs []pushRef) ([]pushStatus, error)
}

// remoteHelper drives the git remote-helper protocol over stdin/stdout.
type remoteHelper struct {
	in      io.Reader
	out     *bufio.Writer
	errw    io.Writer
	backend helperBackend

	progressEnabled bool
}

// run is the main protocol loop, mirroring client.py run(). It reads
// newline-delimited commands from stdin, dispatches capabilities/list/
// option/fetch/push, and processes accumulated fetch/push queues on each
// blank line (batch terminator). It returns a non-nil error for protocol
// violations or backend failures, which the caller maps to an abort exit.
func (h *remoteHelper) run() error {
	scanner := bufio.NewScanner(h.in)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var fetchQ []fetchRef
	var pushQ []pushRef

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "capabilities":
			h.write("list\nfetch\npush\noption\n\n")
		case line == "list":
			if err := h.handleList(false); err != nil {
				return err
			}
		case strings.HasPrefix(line, "list "):
			if err := h.handleList(true); err != nil {
				return err
			}
		case strings.HasPrefix(line, "option"):
			h.handleOption(line)
		case strings.HasPrefix(line, "fetch"):
			fr, ok := parseFetchLine(line)
			if !ok {
				return fmt.Errorf("invalid fetch line: %s", line)
			}
			if !containsFetch(fetchQ, fr) {
				fetchQ = append(fetchQ, fr)
			}
			pushQ = nil
		case strings.HasPrefix(line, "push"):
			pr, ok := parsePushLine(line)
			if !ok {
				return fmt.Errorf("invalid push line: %s", line)
			}
			pushQ = append(pushQ, pr)
			fetchQ = nil
		case line == "":
			if err := h.processBatch(fetchQ, pushQ); err != nil {
				return err
			}
			fetchQ = nil
			pushQ = nil
			h.write("\n")
		default:
			return fmt.Errorf("unknown git command: %s", line)
		}
	}
	return scanner.Err()
}

// handleList services a "list" or "list for-push" command, mirroring
// handle_git_list (client.py). It fetches the ref-list body from the backend
// and writes it followed by a terminating newline.
func (h *remoteHelper) handleList(forPush bool) error {
	body, err := h.backend.list(forPush)
	if err != nil {
		return err
	}
	h.write(body + "\n")
	return nil
}

// handleOption services an "option <name> <value>" command, mirroring the
// option handling in run() (client.py). Only "progress" is recognised; any
// other option replies "unsupported".
func (h *remoteHelper) handleOption(line string) {
	parts := strings.SplitN(line, " ", 3)
	name, value := "", ""
	if len(parts) > 1 {
		name = parts[1]
	}
	if len(parts) > 2 {
		value = parts[2]
	}
	if name == "progress" {
		h.progressEnabled = isProgressTrue(value)
		h.write("ok\n")
		return
	}
	h.write("unsupported\n")
}

// isProgressTrue reports whether value is one of the truthy progress
// spellings accepted by client.py ("true", "1", "yes"), case-insensitively.
func isProgressTrue(value string) bool {
	switch strings.ToLower(value) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

// processBatch dispatches the accumulated fetch and push queues, mirroring
// the empty-line branch of run() (client.py): process_fetch_queue then
// process_push_queue. Push statuses are written as per-ref ok/error lines.
func (h *remoteHelper) processBatch(fetchQ []fetchRef, pushQ []pushRef) error {
	if len(fetchQ) > 0 {
		if err := h.backend.fetch(fetchQ); err != nil {
			return err
		}
	}
	if len(pushQ) > 0 {
		statuses, err := h.backend.push(pushQ)
		if err != nil {
			return err
		}
		for _, s := range statuses {
			if s.ok {
				h.write(fmt.Sprintf("ok %s\n", s.remoteRef))
			} else {
				h.write(fmt.Sprintf("error %s %s\n", s.remoteRef, escapeForStdout(s.msg)))
			}
		}
	}
	return nil
}

// parseFetchLine parses a "fetch <sha> <ref>" line into a fetchRef.
func parseFetchLine(line string) (fetchRef, bool) {
	parts := strings.Fields(line)
	if len(parts) != 3 {
		return fetchRef{}, false
	}
	return fetchRef{sha: parts[1], ref: parts[2]}, true
}

// parsePushLine parses a "push <local>:<remote>" refspec into a pushRef. A
// leading '+' marks a force push; an empty local side marks a deletion.
func parsePushLine(line string) (pushRef, bool) {
	parts := strings.Fields(line)
	if len(parts) != 2 {
		return pushRef{}, false
	}
	local, remote, found := strings.Cut(parts[1], ":")
	if !found {
		return pushRef{}, false
	}
	pr := pushRef{localRef: local, remoteRef: remote}
	if strings.HasPrefix(local, "+") {
		pr.force = true
		pr.localRef = local[1:]
	}
	if pr.localRef == "" {
		pr.deletion = true
	}
	return pr, true
}

// containsFetch reports whether r is already in q (fetch dedup, mirroring
// the "if (sha, ref) not in fetch_queue" guard in client.py run()).
func containsFetch(q []fetchRef, r fetchRef) bool {
	return slices.Contains(q, r)
}

// write writes s to stdout and flushes, mirroring git_stdout.flush() after
// each protocol response in client.py.
func (h *remoteHelper) write(s string) {
	_, _ = h.out.WriteString(s)
	_ = h.out.Flush()
}

// escapeForStdout escapes a value for the git remote-helper protocol,
// mirroring escape_for_stdout (client.py): the value is wrapped in double
// quotes and backslash, double-quote, newline, tab, CR, and any byte outside
// the printable ASCII range (32..126) are escaped.
func escapeForStdout(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r > 0x7e {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
