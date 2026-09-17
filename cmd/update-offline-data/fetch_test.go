// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// retryFixture builds an updater whose waits are recorded rather than slept, so
// the retry policy is exercised without a single real delay.
func retryFixture(t *testing.T, handler http.Handler) (*datasetUpdater, *bytes.Buffer, *[]time.Duration, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	var stdout bytes.Buffer
	var delays []time.Duration
	updater := &datasetUpdater{
		client:  server.Client(),
		verbose: true,
		stdout:  &stdout,
		sleep: func(ctx context.Context, d time.Duration) error {
			delays = append(delays, d)
			return ctx.Err()
		},
	}
	return updater, &stdout, &delays, server.URL
}

// TestFetchSourceRetriesATransientFailure asserts a server that is briefly
// unavailable is retried, with a growing wait between attempts, and that the
// successful body is what the caller gets.
func TestFetchSourceRetriesATransientFailure(t *testing.T) {
	t.Parallel()

	var attempts int
	updater, stdout, delays, url := retryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("catalog"))
	}))

	body, err := updater.fetchSource(context.Background(), "test catalog", url)
	if err != nil {
		t.Fatalf("fetchSource: %v", err)
	}
	if string(body) != "catalog" {
		t.Errorf("body = %q, want the successful attempt", body)
	}
	if attempts != 3 {
		t.Errorf("the server saw %v attempts, want 3", attempts)
	}
	if len(*delays) != 2 {
		t.Fatalf("waited %v times, want 2", len(*delays))
	}
	if (*delays)[0] < fetchBackoffBase {
		t.Errorf("first wait = %v, want at least %v", (*delays)[0], fetchBackoffBase)
	}
	if (*delays)[1] <= (*delays)[0] {
		t.Errorf("waits %v did not grow with each attempt", *delays)
	}
	// -v must let an operator see the same source produce the same fingerprint
	// from one run to the next; that is the evidence that a source is not flaky.
	if !strings.Contains(stdout.String(), "sha256") {
		t.Errorf("the verbose log carries no source fingerprint:\n%v", stdout.String())
	}
}

// TestFetchSourceGivesUpOnAPermanentFailure asserts a 4xx that is not a rate
// limit is answered once: the dataset moved or the request is wrong, and asking
// again cannot help.
func TestFetchSourceGivesUpOnAPermanentFailure(t *testing.T) {
	t.Parallel()

	var attempts int
	updater, _, delays, url := retryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.NotFound(w, r)
	}))

	_, err := updater.fetchSource(context.Background(), "test catalog", url)
	if err == nil {
		t.Fatal("fetchSource accepted a 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want it to name the status", err)
	}
	if attempts != 1 {
		t.Errorf("the server saw %v attempts, want 1", attempts)
	}
	if len(*delays) != 0 {
		t.Errorf("a permanent failure waited %v times, want 0", len(*delays))
	}
}

// TestFetchSourceRejectsATruncatedBody asserts a download that did not arrive
// whole is a failure, not a smaller table. A catalog cut short on a line
// boundary parses cleanly, and rewriting an embedded table from it would look
// exactly like upstream dropping thousands of stations.
func TestFetchSourceRejectsATruncatedBody(t *testing.T) {
	t.Parallel()

	// The handler declares a length it does not deliver.
	updater, _, delays, url := retryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
	}))

	_, err := updater.fetchSource(context.Background(), "test catalog", url)
	if err == nil {
		t.Fatal("fetchSource accepted a truncated body")
	}
	if !strings.Contains(err.Error(), "declared") && !strings.Contains(err.Error(), "reading") {
		t.Errorf("error = %v, want it to say the body was incomplete", err)
	}
	if len(*delays) != fetchAttempts-1 {
		t.Errorf("a truncated body waited %v times, want %v", len(*delays), fetchAttempts-1)
	}
}

// TestFetchSourceRetriesATransportFailure asserts a connection that never
// completes is retried and finally reported with the attempt count, so a source
// that is genuinely down fails the run rather than hanging it.
func TestFetchSourceRetriesATransportFailure(t *testing.T) {
	t.Parallel()

	updater, _, delays, _ := retryFixture(t, http.NotFoundHandler())
	// A closed port is the transport failure: nothing is listening there.
	updater.client = &http.Client{Timeout: 2 * time.Second}

	_, err := updater.fetchSource(context.Background(), "test catalog", "http://127.0.0.1:1/never")
	if err == nil {
		t.Fatal("fetchSource reached a closed port")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("after %v attempts", fetchAttempts)) {
		t.Errorf("error = %v, want it to name the attempt count", err)
	}
	if len(*delays) != fetchAttempts-1 {
		t.Errorf("waited %v times, want %v", len(*delays), fetchAttempts-1)
	}
}

// TestFetchSourceHonoursACancelledContext asserts a caller that has given up is
// not held through the backoff.
func TestFetchSourceHonoursACancelledContext(t *testing.T) {
	t.Parallel()

	updater, _, _, url := retryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := updater.fetchSource(ctx, "test catalog", url)
	if err == nil {
		t.Fatal("fetchSource ignored a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
}

// TestFetchBackoffIsBounded asserts the waits follow an exponential schedule and
// are capped, so a run never spins on a failing source and never waits forever.
func TestFetchBackoffIsBounded(t *testing.T) {
	t.Parallel()

	for attempt := 1; attempt <= 12; attempt++ {
		want := fetchBackoffBase << (attempt - 1)
		if want > fetchBackoffCap || want <= 0 {
			want = fetchBackoffCap
		}
		if got := fetchBackoff(attempt); got != want {
			t.Errorf("fetchBackoff(%v) = %v, want %v", attempt, got, want)
		}
	}
}
