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

// Publishing a release so that one bad upload cannot cost the whole run.
//
// `gh release create <tag> <assets...>` is three requests under the hood: it
// creates the release as a DRAFT, uploads the assets five at a time, then
// publishes — and if any upload fails it deletes the draft it made. So a single
// "HTTP 500: Error saving asset" from GitHub's upload endpoint discards every
// artifact of a run that has just spent minutes building the platform matrix
// and uploading most of a gigabyte, and all it can do is start over.
//
// This file makes those same three steps, on its own terms:
//
//  1. create the release page as a draft (retried);
//  2. upload each artifact with its own gh call (five at a time), retrying only
//     that artifact, and skipping the ones the release already holds
//     byte-for-byte — so a re-run after a failure resumes instead of re-sending
//     gigabytes;
//  3. publish the draft (retried).
//
// Because the release stays a draft until every artifact is up, a failure is
// never visible to users: it leaves a draft that the next run adopts and
// finishes. A PUBLISHED release for the tag is still refused without --force.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// uploadConcurrency is how many artifacts are uploaded at once. gh's own
	// uploader uses the same width; uploads are network-bound, so a few in
	// flight keep the link busy without pressing GitHub's secondary rate limit.
	uploadConcurrency = 5

	// uploadAttempts is how many times one artifact's upload is attempted
	// before the run gives up on that artifact — the same budget prune.go gives
	// a deletion, and with the one-second base about 15s of waiting.
	uploadAttempts = 5

	// releaseAttempts is the attempt budget for the cheap release-level calls
	// (create, edit, delete, publish). Retrying one of those costs seconds,
	// unlike an upload, which re-sends megabytes, so they can afford the same
	// number of tries.
	releaseAttempts = 5
)

// ghFailure is a gh invocation that failed, carrying the command and the
// child's stderr. gh reports the reason — including the API's HTTP status — on
// stderr, and that text is what tells a transient failure from a permanent one.
type ghFailure struct {
	args   []string
	err    error
	stderr string
}

// Error renders the failure as one line: the command, gh's exit status, and the
// stderr gh explained it with.
func (e *ghFailure) Error() string {
	msg := fmt.Sprintf("gh %v: %v", strings.Join(e.args, " "), e.err)
	if s := strings.TrimSpace(e.stderr); s != "" {
		msg += fmt.Sprintf(" (stderr: %v)", s)
	}
	return msg
}

// Unwrap exposes the underlying *exec.ExitError to errors.Is and errors.As.
func (e *ghFailure) Unwrap() error { return e.err }

// gist returns the shortest useful description of a failure, for a progress
// line: what the server said, when gh said anything, else the underlying error.
// It deliberately leaves out the command — ghFailure.Error carries that for the
// one full report a run makes when it gives up.
func gist(err error) string {
	if f, ok := errors.AsType[*ghFailure](err); ok {
		if s := strings.TrimSpace(f.stderr); s != "" {
			return oneLine(errors.New(s))
		}
	}
	return oneLine(err)
}

// oneLine collapses text onto a single line, so that the multi-line stderr gh
// likes to print does not break up a progress line.
func oneLine(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}

// ghHTTPStatusRe matches the HTTP status gh prints for a failed API request,
// for example "HTTP 500: Error saving asset (https://uploads.github.com/...)".
var ghHTTPStatusRe = regexp.MustCompile(`HTTP ([0-9]{3})`)

// permanentGHFailure reports whether a gh failure is a rejection the server
// would repeat, so retrying it would only waste time. The test is deliberately
// narrow — only an HTTP 4xx that is not a throttling response counts — because
// the two mistakes are not equally expensive: giving up on a transient failure
// costs a rebuilt and re-uploaded release, while retrying a permanent one costs
// a few seconds.
func permanentGHFailure(err error) bool {
	var f *ghFailure
	if !errors.As(err, &f) {
		return false
	}
	code, ok := ghHTTPStatus(f.stderr)
	if !ok {
		return false // no status to reason about: assume it is worth retrying
	}
	switch {
	case code < 400 || code >= 500:
		return false // not a 4xx at all: a 5xx is the server having a bad day
	case code == 408, code == 429:
		return false // request timeout, too many requests: both clear on their own
	case code == 403 && mentionsRateLimit(f.stderr):
		return false // throttled, not forbidden: the window resets
	}
	return true
}

// transientGHFailure is permanentGHFailure's complement, in the shape retryer
// wants.
func transientGHFailure(err error) bool { return !permanentGHFailure(err) }

// ghHTTPStatus extracts the HTTP status gh reported, if it reported one.
func ghHTTPStatus(stderr string) (int, bool) {
	m := ghHTTPStatusRe.FindStringSubmatch(stderr)
	if m == nil {
		return 0, false
	}
	code, err := strconv.Atoi(m[1])
	return code, err == nil
}

// mentionsRateLimit reports whether stderr says GitHub refused a request for
// exceeding a rate limit (which clears on its own) rather than because the
// request was forbidden outright.
func mentionsRateLimit(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "rate limit") || strings.Contains(s, "abuse")
}

// ghNotFound reports whether gh's stderr says that what was asked for does not
// exist. gh says "release not found" for a missing release and "Not Found" for
// a missing git ref, so the test is case-insensitive and also accepts a bare
// status code.
func ghNotFound(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "not found") || strings.Contains(s, "404")
}

// runGH runs a gh command, capturing stderr so that a failure can be told
// transient from permanent. stdout goes to out, or nowhere when out is nil:
// the per-artifact progress lines this tool prints say more than gh's own
// upload chatter does.
func runGH(out io.Writer, args ...string) error {
	if out == nil {
		out = io.Discard
	}
	var stderr bytes.Buffer
	cmd := exec.Command("gh", args...)
	cmd.Stdout = out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return &ghFailure{args: args, err: err, stderr: stderr.String()}
	}
	return nil
}

// ghRetry runs a gh command with the retry policy for release-level calls,
// which are cheap enough to simply try again. what names the operation in the
// retry notices.
func ghRetry(what string, progress io.Writer, args ...string) error {
	return retryer{
		what:      what,
		attempts:  releaseAttempts,
		base:      time.Second,
		transient: transientGHFailure,
		report:    reporter(progress),
	}.do(func() error {
		return runGH(progress, args...)
	})
}

// releaseState is what GitHub already has for a tag.
type releaseState struct {
	// exists reports that a release is there, draft or published.
	exists bool
	// draft reports that it is an unpublished draft: unfinished work from an
	// earlier run of this tool, which a re-run adopts and finishes.
	draft bool
	// digests maps the name of each asset already attached to the release to
	// the digest GitHub reports for the bytes it stored, so a re-run can tell
	// which of its artifacts are already up. GitHub reports it as
	// "sha256:<hex>"; an asset with no digest (an older asset, or a gh that
	// does not surface one) is simply always uploaded again.
	digests map[string]string
}

// isPublished reports whether users can already see a release for this tag.
func (s releaseState) isPublished() bool { return s.exists && !s.draft }

// uploadedIdentical reports whether the release already holds an asset named
// name whose stored bytes are byte-for-byte the file at path.
//
// The comparison is by sha256 rather than size because the published release
// notes list a sha256 for every artifact: an artifact left in place has to be
// exactly the file those notes describe, or the checksum table would be wrong.
// Hashing the local file is only ever done for names the release already has,
// which is the resume case alone — a fresh publish hashes nothing here.
func (s releaseState) uploadedIdentical(name, path string) bool {
	want := strings.TrimPrefix(s.digests[name], "sha256:")
	if want == "" {
		return false
	}
	sum, err := sha256sum(path)
	if err != nil {
		return false
	}
	return want == sum
}

// releaseStateFor reports what, if anything, already exists for tag.
//
// The lookup must see drafts. gh's `release view` merges the REST lookup of
// releases/tags/<tag> — which cannot return a draft — with a GraphQL lookup by
// pending tag name, and reports isDraft; that is what lets a re-run adopt the
// draft an interrupted run left behind instead of finding the tag taken.
func releaseStateFor(tag string) (releaseState, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("gh", "release", "view", tag, "--json", "isDraft,assets")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ghNotFound(stderr.String()) {
			return releaseState{}, nil // no release for this tag yet
		}
		return releaseState{}, fmt.Errorf("check existing release %v: %w", tag,
			&ghFailure{args: []string{"release", "view", tag}, err: err, stderr: stderr.String()})
	}
	var view struct {
		IsDraft bool `json:"isDraft"`
		Assets  []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
		return releaseState{}, fmt.Errorf("parse release %v: %w", tag, err)
	}
	st := releaseState{
		exists:  true,
		draft:   view.IsDraft,
		digests: make(map[string]string, len(view.Assets)),
	}
	for _, a := range view.Assets {
		st.digests[a.Name] = a.Digest
	}
	return st, nil
}

// ensureDraftRelease creates the release page for tag as an unpublished draft,
// or refreshes the draft an earlier interrupted run left behind.
//
// The tag is pushed before this runs, so --verify-tag makes gh use the tag that
// was just created rather than minting one from the default branch: this tool
// never wants a tag created implicitly, because the tag is what fixes the
// module source that downstream `go mod tidy` resolves.
func ensureDraftRelease(tag, notes string, st releaseState, progress io.Writer) error {
	if st.exists && st.draft {
		mustFprintf(progress, "Resuming draft release %v (%v asset(s) already attached).\n",
			tag, len(st.digests))
		// Refresh the title and notes, so the page matches the artifacts this
		// run ends up uploading even if an earlier attempt died before writing
		// them.
		return ghRetry("refresh draft release "+tag, progress,
			"release", "edit", tag, "--title", tag, "--notes", notes)
	}
	err := ghRetry("create draft release "+tag, progress,
		"release", "create", tag, "--title", tag, "--notes", notes, "--draft", "--verify-tag")
	if err == nil {
		return nil
	}
	// The draft may exist even though this call failed: a request can be
	// committed remotely and only its response lost. Ask before reporting a
	// failure for work that is in fact done.
	if again, qerr := releaseStateFor(tag); qerr == nil && again.exists && again.draft {
		mustFprintf(progress, "Draft release %v already exists despite the error above; continuing.\n", tag)
		return nil
	}
	return err
}

// publishRelease flips the draft release to published, the last remote step
// before the retention prune.
//
// --draft=false with no --latest is exactly the request gh itself makes when it
// publishes the draft it created for an upload: GitHub's "latest" default is
// left to the server, so a release is marked latest by the same rule as before.
// If this fails for good, the release is still a draft holding every artifact,
// and re-running the tool publishes it — nothing is lost.
func publishRelease(tag string, progress io.Writer) error {
	mustFprintf(progress, "Publishing release %v...\n", tag)
	return ghRetry("publish release "+tag, progress, "release", "edit", tag, "--draft=false")
}

// uploader uploads one release's artifacts, one gh call per artifact, retrying
// each artifact on its own.
type uploader struct {
	tag         string
	concurrency int           // how many uploads run at once
	attempts    int           // attempts per artifact
	base        time.Duration // first backoff between those attempts
	progress    io.Writer
}

// run uploads every path in assets to the draft release for u.tag.
//
// Artifacts the release already holds byte-for-byte are left alone, which is
// what makes a re-run after a failed upload resume rather than re-send
// gigabytes; everything else goes up with --clobber, so an artifact rebuilt
// from changed source replaces the earlier copy and the release notes' checksum
// table always describes the bytes that are really there. Each artifact is
// attempted independently: one that keeps failing is reported and the rest
// still go up, and the release is left as a draft for the next run to finish.
func (u uploader) run(assets []string, st releaseState) error {
	var todo []string
	var kept int
	for _, path := range assets {
		if st.uploadedIdentical(filepath.Base(path), path) {
			kept++
			continue
		}
		todo = append(todo, path)
	}
	if kept > 0 {
		mustFprintf(u.progress, "%v of %v asset(s) are already uploaded unchanged; uploading %v.\n",
			kept, len(assets), len(todo))
	}
	if len(todo) == 0 {
		mustFprintf(u.progress, "All %v asset(s) of %v are already uploaded.\n", len(assets), u.tag)
		return nil
	}
	concurrency := max(1, min(u.concurrency, len(todo)))
	mustFprintf(u.progress, "Uploading %v asset(s) to %v, %v at a time...\n",
		len(todo), u.tag, concurrency)

	// out serializes the progress lines: the workers below, and the retry
	// notices raised inside them, all write here at once, and a line that
	// interleaves with another is unreadable.
	out := &syncWriter{w: u.progress}
	start := time.Now()

	var mu sync.Mutex // guards the counters and the failure list
	var done, uploaded int
	var uploadedBytes int64
	var failed []string

	jobs := make(chan string)
	var wg sync.WaitGroup
	for range concurrency {
		wg.Go(func() {
			for path := range jobs {
				name := filepath.Base(path)
				err := u.uploadOne(out, path)
				mu.Lock()
				done++
				if err != nil {
					failed = append(failed, name)
					mustFprintf(out, "  [%v/%v] FAILED %v: %v\n", done, len(todo), name, gist(err))
				} else {
					uploaded++
					uploadedBytes += localSize(path)
					mustFprintf(out, "  [%v/%v] %v\n", done, len(todo), name)
				}
				mu.Unlock()
			}
		})
	}
	for _, path := range todo {
		jobs <- path
	}
	close(jobs)
	wg.Wait()

	if len(failed) > 0 {
		sort.Strings(failed)
		return fmt.Errorf(
			"%v of %v asset(s) could not be uploaded after %v attempt(s) each: %v; "+
				"release %v is left as a DRAFT holding %v asset(s) — re-run "+
				"scripts/publish-github-release-artifacts.sh to resume the upload and "+
				"publish it (no --force needed)",
			len(failed), len(todo), u.attempts, strings.Join(failed, ", "), u.tag, uploaded+kept)
	}
	mustFprintf(u.progress, "Uploaded %v asset(s) (%v) in %v.\n",
		uploaded, humanBytes(uploadedBytes), time.Since(start).Round(time.Second))
	return nil
}

// uploadOne uploads a single artifact to the release, retrying transient
// failures with exponential backoff.
//
// --clobber makes each attempt idempotent: an attempt that re-sends an artifact
// the server did in fact store (the response to the first try was lost, say)
// replaces it rather than being rejected as a duplicate, and a retry after a
// half-written upload does not leave a broken asset behind.
func (u uploader) uploadOne(progress io.Writer, path string) error {
	return retryer{
		what:      filepath.Base(path),
		attempts:  u.attempts,
		base:      u.base,
		transient: transientGHFailure,
		report:    reporter(progress),
	}.do(func() error {
		return runGH(nil, "release", "upload", u.tag, path, "--clobber")
	})
}

// localSize returns the size of the file at path, or 0 if it cannot be read.
// It only feeds the byte total in the upload report; an unreadable artifact is
// reported by its upload, which is the place that can say what went wrong.
func localSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// syncWriter serializes writes, so progress lines from concurrent uploads do
// not interleave mid-line.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// Write writes p to the underlying writer with the lock held.
func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
