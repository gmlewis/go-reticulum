// Copyright 2026 Glenn Lewis. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

// Retrying failed GitHub API calls.
//
// GitHub's upload endpoint intermittently answers a perfectly good asset upload
// with "HTTP 500: Error saving asset". A publish that dies on one such hiccup
// throws away the whole run: every artifact was just built (minutes) and most
// of a gigabyte was already uploaded, and nothing is left behind to resume
// from. So every remote step of a publish goes through the retryer below,
// which retries only failures that are plausibly transient (see
// permanentGHFailure in upload.go) and waits longer between each attempt —
// 1s, 2s, 4s, then 8s.
//
// The same backoff schedule paces the per-asset deletion loop in prune.go.
package main

import (
	"io"
	"time"
)

// maxBackoffShift caps retryBackoff's doubling. At the one-second base the
// callers use, base<<15 is over nine hours, far beyond any budget here; the cap
// exists only so a future caller with a huge attempt count cannot shift its way
// into an overflowed (negative) duration.
const maxBackoffShift = 15

// retryBackoff returns how long to wait before the given retry, where retry 1
// is the wait after a first failed attempt: one base, then doubling thereafter
// (with a one-second base: 1s, 2s, 4s, 8s).
func retryBackoff(retry int, base time.Duration) time.Duration {
	if retry < 1 {
		return 0
	}
	if retry > maxBackoffShift {
		retry = maxBackoffShift
	}
	return base << (retry - 1)
}

// retryer retries a single remote operation with exponential backoff.
type retryer struct {
	// what names the operation in the retry notices, as the object of
	// "retrying <what>": "upload of gorrcd-0.131.0-linux-amd64", say.
	what string
	// attempts is the total number of attempts, including the first.
	attempts int
	// base is the first backoff; every later retry waits twice as long.
	base time.Duration
	// transient reports whether a failure is worth retrying. A failure it
	// rejects is returned at once: the server would answer the same way again,
	// so the remaining attempts would only burn time.
	transient func(error) bool
	// report receives one line per retry, so a long upload that is sitting
	// still says why. A nil report keeps it quiet.
	report func(format string, args ...any)
}

// do runs op until it succeeds, fails in a way retrying cannot fix, or runs out
// of attempts. The error returned is the one from the last attempt, unchanged,
// so the caller sees what the server actually said.
func (r retryer) do(op func() error) error {
	attempts := max(1, r.attempts)
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			d := retryBackoff(attempt-1, r.base)
			r.notice("retrying %v in %v (attempt %v of %v): %v\n",
				r.what, d, attempt, attempts, gist(err))
			time.Sleep(d)
		}
		if err = op(); err == nil {
			return nil
		}
		if !r.retryable(err) {
			return err
		}
	}
	return err
}

// retryable reports whether a failure should be retried. A retryer with no
// predicate retries everything it is given.
func (r retryer) retryable(err error) bool {
	if r.transient == nil {
		return true
	}
	return r.transient(err)
}

// notice reports a retry, if a reporter was supplied.
func (r retryer) notice(format string, args ...any) {
	if r.report != nil {
		r.report(format, args...)
	}
}

// reporter turns a progress writer into the callback retryer and uploader
// expect. A nil writer is accepted so a caller without one is silent rather
// than panicking.
func reporter(progress io.Writer) func(format string, args ...any) {
	if progress == nil {
		progress = io.Discard
	}
	return func(format string, args ...any) { mustFprintf(progress, format, args...) }
}
