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

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRetryBackoff pins the backoff schedule: one base before the first retry,
// doubling thereafter, and a hard cap on the doubling.
func TestRetryBackoff(t *testing.T) {
	t.Parallel()

	cases := []struct {
		retry int
		want  time.Duration
	}{
		{0, 0}, // no retry yet: nothing to wait for
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 16 * time.Second},
		{maxBackoffShift, time.Second << (maxBackoffShift - 1)},
		{maxBackoffShift + 100, time.Second << (maxBackoffShift - 1)}, // clamped, never overflowed
	}
	for _, c := range cases {
		if got := retryBackoff(c.retry, time.Second); got != c.want {
			t.Errorf("retryBackoff(%v, 1s) = %v, want %v", c.retry, got, c.want)
		}
	}
	// A sub-second base scales the same way, which is what lets the tests below
	// retry without sleeping for seconds.
	if got := retryBackoff(3, time.Millisecond); got != 4*time.Millisecond {
		t.Errorf("retryBackoff(3, 1ms) = %v, want 4ms", got)
	}
}

// TestRetryer pins the retry loop's contract: a transient failure is retried
// up to the attempt budget and the last error is returned when the budget runs
// out; a failure the predicate rejects (a permanent 4xx, in the publish path)
// is returned at once; and every retry is reported with the wait and the
// attempt number, since a run sitting in a 8s backoff otherwise looks hung.
func TestRetryer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		desc         string
		attempts     int
		failures     int  // attempts that fail, before op starts succeeding
		transient    bool // whether the predicate says those failures are retryable
		wantCalls    int
		wantNotices  int
		wantNilError bool
	}{
		{"succeeds at once", 5, 0, true, 1, 0, true},
		{"succeeds on the third attempt", 5, 2, true, 3, 2, true},
		{"last attempt is the one that succeeds", 3, 2, true, 3, 2, true},
		{"budget runs out", 3, 99, true, 3, 2, false},
		{"permanent failure is not retried", 5, 1, false, 1, 0, false},
		{"one attempt only", 1, 1, true, 1, 0, false},
		{"zero attempts still tries once", 0, 1, true, 1, 0, false},
	}
	for _, c := range cases {
		var calls int
		var notices bytes.Buffer
		err := retryer{
			what:      "upload of gorrcd-0.131.0-linux-amd64",
			attempts:  c.attempts,
			base:      time.Millisecond,
			transient: func(error) bool { return c.transient },
			report:    func(format string, args ...any) { fmt.Fprintf(&notices, format, args...) },
		}.do(func() error {
			calls++
			if calls <= c.failures {
				return &ghFailure{
					args:   []string{"release", "upload"},
					err:    errors.New("exit status 1"),
					stderr: "HTTP 500: Error saving asset (https://uploads.github.com/...)",
				}
			}
			return nil
		})

		if calls != c.wantCalls {
			t.Errorf("%v: op called %v time(s), want %v", c.desc, calls, c.wantCalls)
		}
		if (err == nil) != c.wantNilError {
			t.Errorf("%v: error = %v, want nil error: %v", c.desc, err, c.wantNilError)
		}
		if got := strings.Count(notices.String(), "retrying"); got != c.wantNotices {
			t.Errorf("%v: reported %v retry notice(s), want %v (%q)",
				c.desc, got, c.wantNotices, notices.String())
		}
	}

	// The notice names the operation, the wait, and where it is in the budget,
	// and it keeps the error on one line.
	var notices bytes.Buffer
	_ = retryer{
		what:      "upload of gorrcd-0.131.0-linux-amd64",
		attempts:  5,
		base:      time.Millisecond,
		transient: func(error) bool { return true },
		report:    func(format string, args ...any) { fmt.Fprintf(&notices, format, args...) },
	}.do(func() error {
		return &ghFailure{
			args:   []string{"release", "upload", "v0.131.0", "/tmp/gorrcd"},
			err:    errors.New("exit status 1"),
			stderr: "HTTP 500: Error saving asset\n  (uploads.github.com)",
		}
	})
	got := notices.String()
	for _, want := range []string{
		"retrying upload of gorrcd-0.131.0-linux-amd64 in 1ms",
		"attempt 2 of 5",
		"HTTP 500: Error saving asset (uploads.github.com)", // newline collapsed
	} {
		if !strings.Contains(got, want) {
			t.Errorf("retry notice %q does not contain %q", got, want)
		}
	}
	if strings.Count(got, "\n") != 4 {
		t.Errorf("retry notices = %q, want one line per retry", got)
	}
}

// TestPermanentGHFailure pins which gh failures are worth retrying, using the
// stderr gh actually prints. The asymmetric cost is the point: treating a
// transient failure as permanent throws away a rebuilt release, while treating
// a permanent one as transient costs a few wasted seconds — so only an HTTP 4xx
// that is not a throttling response may count as permanent.
func TestPermanentGHFailure(t *testing.T) {
	t.Parallel()

	cases := []struct {
		desc   string
		stderr string
		want   bool
	}{
		{
			// The failure that motivated all of this.
			"the upload 500 this tool has hit",
			"HTTP 500: Error saving asset (https://uploads.github.com/repos/gmlewis/go-reticulum/releases/391019507/assets?label=&name=golxmd-0.131.0-linux-amd64)",
			false,
		},
		{"bad gateway", "HTTP 502: Bad Gateway (https://api.github.com/)", false},
		{"service unavailable", "HTTP 503: Service Unavailable", false},
		{"a 3xx gh followed and gave up on", "HTTP 301: Moved Permanently", false},
		{"rate limited", "HTTP 403: API rate limit exceeded for user ID 12345. (https://api.github.com/)", false},
		{"secondary rate limit", "HTTP 403: You have exceeded a secondary rate limit. Please wait a few minutes before you try again.", false},
		{"too many requests", "HTTP 429: Too Many Requests", false},
		{"request timeout", "HTTP 408: Request Timeout", false},
		{"no status to reason about", "dial tcp: lookup api.github.com: no such host", false},
		{"no HTTP status at all", "failed to run git: exit status 128", false},
		{"validation failure", "HTTP 422: Validation Failed (https://api.github.com/repos/gmlewis/go-reticulum/releases)", true},
		{"already exists", "HTTP 422: Validation Failed (already_exists)", true},
		{"not found", "HTTP 404: Not Found (https://api.github.com/repos/gmlewis/go-reticulum/releases/tags/v0.131.0)", true},
		{"bad credentials", "HTTP 401: Bad credentials (https://api.github.com/)", true},
		{"a 403 that is not about rate limits", "HTTP 403: Resource not accessible by integration", true},
		{"bad request", "HTTP 400: Bad Request", true},
	}
	for _, c := range cases {
		err := &ghFailure{args: []string{"release", "upload"}, err: errors.New("exit status 1"), stderr: c.stderr}
		if got := permanentGHFailure(err); got != c.want {
			t.Errorf("%v: permanentGHFailure() = %v, want %v (stderr: %v)", c.desc, got, c.want, c.stderr)
		}
		if got := transientGHFailure(err); got == c.want {
			t.Errorf("%v: transientGHFailure() = %v, want %v (it must complement permanentGHFailure)",
				c.desc, got, !c.want)
		}
	}

	// An error that is not a gh failure carries no status, so it is retried:
	// the alternative is giving up on a failure nobody classified.
	if permanentGHFailure(errors.New("some other error")) {
		t.Error("permanentGHFailure(non-gh error) = true, want false (unclassified failures are retried)")
	}
	if f := (&ghFailure{args: []string{"release", "upload"}, err: errors.New("exit status 1"), stderr: "boom"}); !strings.Contains(f.Error(), "boom") {
		t.Errorf("ghFailure.Error() = %q, want it to include the stderr", f.Error())
	}
}

// TestReleaseStateFor checks the lookup that decides whether a run creates a
// fresh draft, adopts an interrupted one, or refuses a published release.
// Drafts matter most: gh's release view finds them through a GraphQL lookup
// (the REST endpoint cannot return a draft), and a draft is what a re-run
// resumes from.
func TestReleaseStateFor(t *testing.T) {
	logPath := fakeGH(t)

	t.Setenv("GH_FAKE_VIEW_JSON",
		`{"isDraft":true,"assets":[{"name":"gorrcd-0.131.0-linux-amd64","digest":"sha256:aa"},`+
			`{"name":"gorrcd-0.131.0-darwin-arm64","digest":""}]}`)
	st, err := releaseStateFor("v0.131.0")
	if err != nil {
		t.Fatalf("releaseStateFor: %v", err)
	}
	if !st.exists || !st.draft {
		t.Errorf("releaseStateFor(draft) = {exists:%v draft:%v}, want both true", st.exists, st.draft)
	}
	if st.isPublished() {
		t.Error("releaseStateFor(draft).isPublished() = true, want false")
	}
	if got := st.digests["gorrcd-0.131.0-linux-amd64"]; got != "sha256:aa" {
		t.Errorf("digest of gorrcd-0.131.0-linux-amd64 = %q, want %q", got, "sha256:aa")
	}
	if _, ok := st.digests["gorrcd-0.131.0-windows-amd64.exe"]; ok {
		t.Error("releaseStateFor reported an asset the release does not have")
	}

	// A published release is reported as published, which is what makes a
	// re-run refuse the tag unless --force.
	t.Setenv("GH_FAKE_VIEW_JSON", `{"isDraft":false,"assets":[]}`)
	st, err = releaseStateFor("v0.131.0")
	if err != nil {
		t.Fatalf("releaseStateFor: %v", err)
	}
	if !st.isPublished() {
		t.Errorf("releaseStateFor(published) = {exists:%v draft:%v}, want a published release",
			st.exists, st.draft)
	}

	// No release for the tag: gh says so on stderr, and that is not an error —
	// it is the ordinary state of a version that has never been published.
	t.Setenv("GH_FAKE_VIEW_STDERR", "release not found")
	t.Setenv("GH_FAKE_VIEW_JSON", "")
	st, err = releaseStateFor("v0.132.0")
	if err != nil {
		t.Fatalf("releaseStateFor(missing): %v", err)
	}
	if st.exists || st.draft || st.digests != nil {
		t.Errorf("releaseStateFor(missing) = %+v, want the zero state", st)
	}

	// Any other failure must be reported, not read as "no release": creating a
	// release for a tag that does exist would fail confusingly later on.
	t.Setenv("GH_FAKE_VIEW_STDERR", "HTTP 500: Server Error")
	if _, err := releaseStateFor("v0.131.0"); err == nil {
		t.Error("releaseStateFor(server error) = nil error, want an error")
	}

	// Unparsable output is an error too, rather than a silently empty state
	// that would re-upload everything.
	t.Setenv("GH_FAKE_VIEW_STDERR", "")
	t.Setenv("GH_FAKE_VIEW_JSON", "not json")
	if _, err := releaseStateFor("v0.131.0"); err == nil {
		t.Error("releaseStateFor(bad json) = nil error, want an error")
	}

	if calls := ghCalls(t, logPath); len(calls) != 5 {
		t.Errorf("gh was called %v time(s), want 5: %v", len(calls), calls)
	}
}

// TestUploaderResumesAndRetries exercises the upload pass against a stand-in
// gh: an artifact the release already holds byte-for-byte is skipped (that is
// the resume), one that fails twice with the HTTP 500 this tool hit is retried
// until it goes up, and one rejected outright (a 422, which retrying cannot
// fix) fails after a single attempt. The release is left as a draft holding
// what did arrive, so the re-run the error message suggests can finish it.
func TestUploaderResumesAndRetries(t *testing.T) {
	logPath := fakeGH(t)
	dir := t.TempDir()

	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %v: %v", name, err)
		}
		return path
	}
	keep := write("gorrcd-0.131.0-linux-amd64", "already uploaded\n")
	retry := write("gorrcd-0.131.0-darwin-arm64", "upload me\n")
	fail := write("golxmd-0.131.0-linux-amd64", "rejected\n")

	// The release already holds the first artifact, byte for byte, and has
	// stale entries for the other two (an earlier run uploaded different
	// builds), so only the first may be skipped.
	keepSum, err := sha256sum(keep)
	if err != nil {
		t.Fatalf("sha256sum: %v", err)
	}
	st := releaseState{
		exists: true,
		draft:  true,
		digests: map[string]string{
			filepath.Base(keep):  "sha256:" + keepSum,
			filepath.Base(retry): "sha256:stale",
			filepath.Base(fail):  "sha256:stale",
		},
	}

	t.Setenv("GH_FAKE_TRANSIENT_ASSET", filepath.Base(retry))
	t.Setenv("GH_FAKE_TRANSIENT_TIMES", "2")
	t.Setenv("GH_FAKE_PERMANENT_ASSET", filepath.Base(fail))

	var out bytes.Buffer
	err = uploader{
		tag:         "v0.131.0",
		concurrency: 2,
		attempts:    uploadAttempts,
		base:        time.Millisecond,
		progress:    &out,
	}.run([]string{keep, retry, fail}, st)

	if err == nil {
		t.Fatal("uploader.run() = nil error, want the rejected artifact reported")
	}
	for _, want := range []string{
		"1 of 2 asset(s) could not be uploaded",
		"after 5 attempt(s) each",
		filepath.Base(fail),
		"left as a DRAFT",
		// The suggested recovery is a plain re-run: --force would discard the
		// artifacts the run did manage to upload.
		"re-run scripts/publish-github-release-artifacts.sh",
		"no --force needed",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("upload error %q does not mention %q", err, want)
		}
	}

	calls := ghCalls(t, logPath)
	if got := countCalls(calls, filepath.Base(keep)); got != 0 {
		t.Errorf("the artifact already uploaded byte-for-byte was uploaded %v time(s), want 0", got)
	}
	// Two failures (HTTP 500) then the success: the artifact that was failing
	// transiently does go up, and it is retried, not given up on.
	if got := countCalls(calls, filepath.Base(retry)); got != 3 {
		t.Errorf("the transiently-failing artifact was uploaded %v time(s), want 3 (two 500s, then success)", got)
	}
	// A 422 is permanent, so the rejected artifact is attempted exactly once:
	// no amount of backoff would change the answer.
	if got := countCalls(calls, filepath.Base(fail)); got != 1 {
		t.Errorf("the rejected artifact was uploaded %v time(s), want 1 (a 422 is not retried)", got)
	}

	report := out.String()
	for _, want := range []string{
		"1 of 3 asset(s) are already uploaded unchanged; uploading 2.",
		"[1/2]", "[2/2]",
		"retrying " + filepath.Base(retry) + " in 1ms",
		"retrying " + filepath.Base(retry) + " in 2ms",
		"FAILED " + filepath.Base(fail) + ": HTTP 422",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("upload report does not contain %q:\n%v", want, report)
		}
	}
	if strings.Contains(report, "already uploaded unchanged; uploading 3") {
		t.Error("the upload pass did not skip the artifact the release already holds")
	}
}

// TestUploaderSuccess covers the ordinary path: every artifact goes up (the
// release starts with none), a transient failure is absorbed by the retries,
// and the run reports what it uploaded.
func TestUploaderSuccess(t *testing.T) {
	logPath := fakeGH(t)
	dir := t.TempDir()

	var assets []string
	for _, name := range []string{"gorrcd-0.131.0-linux-amd64", "gorrcd-0.131.0-darwin-arm64"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("binary "+name), 0o644); err != nil {
			t.Fatalf("write %v: %v", name, err)
		}
		assets = append(assets, path)
	}
	t.Setenv("GH_FAKE_TRANSIENT_ASSET", filepath.Base(assets[1]))
	t.Setenv("GH_FAKE_TRANSIENT_TIMES", "1")

	var out bytes.Buffer
	err := uploader{
		tag:         "v0.131.0",
		concurrency: uploadConcurrency,
		attempts:    uploadAttempts,
		base:        time.Millisecond,
		progress:    &out,
	}.run(assets, releaseState{})

	if err != nil {
		t.Fatalf("uploader.run() = %v, want nil", err)
	}
	if got := len(ghCalls(t, logPath)); got != 3 {
		t.Errorf("gh was called %v time(s), want 3 (one per artifact, plus the retry)", got)
	}
	for _, want := range []string{
		"Uploading 2 asset(s) to v0.131.0, 2 at a time...",
		"[1/2]", "[2/2]",
		"retrying " + filepath.Base(assets[1]) + " in 1ms",
		"Uploaded 2 asset(s)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("upload report does not contain %q:\n%v", want, out.String())
		}
	}
}

// fakeGH puts a stand-in gh on PATH, so the publish path can be exercised
// without touching GitHub, and returns the path of the log that records every
// gh invocation the test causes.
//
// The stand-in answers `release view` from GH_FAKE_VIEW_JSON, failing with
// GH_FAKE_VIEW_STDERR when that is set; and answers `release upload` by
// rejecting the artifact named GH_FAKE_PERMANENT_ASSET with a 422 every time,
// and failing the one named GH_FAKE_TRANSIENT_ASSET with the HTTP 500 this tool
// hit for its first GH_FAKE_TRANSIENT_TIMES attempts.
//
// It sets PATH for the whole process, so a test using it must not be parallel.
func fakeGH(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "gh.log")
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(fakeGHBody), 0o755); err != nil {
		t.Fatalf("write stand-in gh: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_FAKE_LOG", logPath)
	t.Setenv("GH_FAKE_COUNT", filepath.Join(dir, "transient-attempts"))
	return logPath
}

// fakeGHBody is the stand-in gh: it logs its arguments, then answers the two
// subcommands the publish path uses from the scenario in its environment.
const fakeGHBody = `#!/bin/sh
printf '%s\n' "$*" >> "${GH_FAKE_LOG}"
case "$1 $2" in
"release view")
	if [ -n "${GH_FAKE_VIEW_STDERR}" ]; then
		printf '%s\n' "${GH_FAKE_VIEW_STDERR}" >&2
		exit 1
	fi
	printf '%s\n' "${GH_FAKE_VIEW_JSON}"
	exit 0
	;;
"release upload")
	# The artifact is the last argument that is not the --clobber flag.
	asset=""
	for a in "$@"; do
		if [ "$a" != "--clobber" ]; then
			asset="$a"
		fi
	done
	asset="${asset##*/}"
	if [ -n "${GH_FAKE_PERMANENT_ASSET}" ] && [ "${asset}" = "${GH_FAKE_PERMANENT_ASSET}" ]; then
		printf 'HTTP 422: Validation Failed (already_exists)\n' >&2
		exit 1
	fi
	if [ -n "${GH_FAKE_TRANSIENT_ASSET}" ] && [ "${asset}" = "${GH_FAKE_TRANSIENT_ASSET}" ]; then
		tries=0
		if [ -f "${GH_FAKE_COUNT}" ]; then
			tries=$(cat "${GH_FAKE_COUNT}")
		fi
		if [ "${tries}" -lt "${GH_FAKE_TRANSIENT_TIMES}" ]; then
			echo $((tries + 1)) > "${GH_FAKE_COUNT}"
			printf 'HTTP 500: Error saving asset (https://uploads.github.com/repos/o/r/releases/1/assets?label=&name=%s)\n' "${asset}" >&2
			exit 1
		fi
	fi
	exit 0
	;;
esac
exit 0
`

// ghCalls returns the gh invocations recorded in the stand-in's log, one
// string per call.
func ghCalls(t *testing.T, logPath string) []string {
	t.Helper()

	data, err := os.ReadFile(logPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatalf("read gh log: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// countCalls counts the logged gh invocations that mention substr.
func countCalls(calls []string, substr string) int {
	var n int
	for _, c := range calls {
		if strings.Contains(c, substr) {
			n++
		}
	}
	return n
}
