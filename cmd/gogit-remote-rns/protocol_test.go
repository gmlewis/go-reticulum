// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// fakeBackend is a test double for helperBackend. It records calls and
// returns canned responses so the remote-helper protocol loop can be
// exercised without a network or a running RNS link.
type fakeBackend struct {
	listRes   string
	listErr   error
	listCalls []bool

	fetchRefs []fetchRef
	fetchErr  error

	pushRefs []pushRef
	pushRes  []pushStatus
	pushErr  error
}

func (f *fakeBackend) list(forPush bool) (string, error) {
	f.listCalls = append(f.listCalls, forPush)
	return f.listRes, f.listErr
}

func (f *fakeBackend) fetch(refs []fetchRef) error {
	f.fetchRefs = refs
	return f.fetchErr
}

func (f *fakeBackend) push(refs []pushRef) ([]pushStatus, error) {
	f.pushRefs = refs
	return f.pushRes, f.pushErr
}

// newHelper builds a remoteHelper reading from input, writing to a buffer,
// and backed by b. It returns the helper and the output buffer.
func newHelper(input string, b *fakeBackend) (*remoteHelper, *bytes.Buffer) {
	out := &bytes.Buffer{}
	h := &remoteHelper{
		in:      bufio.NewReader(strings.NewReader(input)),
		out:     bufio.NewWriter(out),
		errw:    &bytes.Buffer{},
		backend: b,
	}
	return h, out
}

// TestCapabilities checks the capabilities advertisement: list, fetch, push,
// option, then a blank line (client.py run()).
func TestCapabilities(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{}
	h, out := newHelper("capabilities\n", b)
	if err := h.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "list\nfetch\npush\noption\n\n"
	if got := out.String(); got != want {
		t.Errorf("capabilities output = %q, want %q", got, want)
	}
}

// TestList checks the list command calls backend.list(false) and writes the
// ref-list body followed by a terminating newline.
func TestList(t *testing.T) {
	t.Parallel()

	body := "abcdef0123456789abcdef0123456789abcdef01 refs/heads/main\n" +
		"1234567890abcdef1234567890abcdef12345678 refs/heads/dev\n" +
		"@refs/heads/main HEAD"
	b := &fakeBackend{listRes: body}
	h, out := newHelper("list\n", b)
	if err := h.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(b.listCalls) != 1 || b.listCalls[0] {
		t.Errorf("list calls = %v, want [false]", b.listCalls)
	}
	if got := out.String(); got != body+"\n" {
		t.Errorf("list output = %q, want %q", got, body+"\n")
	}
}

// TestListForPush checks "list for-push" calls backend.list(true).
func TestListForPush(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{listRes: ""}
	h, _ := newHelper("list for-push\n", b)
	if err := h.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(b.listCalls) != 1 || !b.listCalls[0] {
		t.Errorf("list calls = %v, want [true]", b.listCalls)
	}
}

// TestListError checks a backend.list error aborts the helper.
func TestListError(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{listErr: errBadList}
	h, _ := newHelper("list\n", b)
	if err := h.run(); err != errBadList {
		t.Errorf("run err = %v, want errBadList", err)
	}
}

// TestOptionProgress checks the option progress command sets the flag and
// replies ok; unknown options reply unsupported.
func TestOptionProgress(t *testing.T) {
	t.Parallel()

	cases := []struct {
		line   string
		want   string
		enable bool
	}{
		{"option progress true", "ok\n", true},
		{"option progress 1", "ok\n", true},
		{"option progress yes", "ok\n", true},
		{"option progress false", "ok\n", false},
		{"option verbosity 3", "unsupported\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			t.Parallel()

			b := &fakeBackend{}
			h, out := newHelper(tc.line+"\n", b)
			if err := h.run(); err != nil {
				t.Fatalf("run: %v", err)
			}
			if got := out.String(); got != tc.want {
				t.Errorf("option output = %q, want %q", got, tc.want)
			}
			if h.progressEnabled != tc.enable {
				t.Errorf("progressEnabled = %v, want %v", h.progressEnabled, tc.enable)
			}
		})
	}
}

// TestFetchBatch checks fetch lines accumulate into a queue, dedup, and are
// dispatched on the empty line; push queue is cleared by a fetch line.
func TestFetchBatch(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{}
	h, out := newHelper(
		"capabilities\n"+
			"list\n"+
			"fetch sha1 refs/heads/main\n"+
			"fetch sha2 refs/heads/dev\n"+
			"fetch sha1 refs/heads/main\n"+
			"\n",
		b,
	)
	if err := h.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	wantRefs := []fetchRef{
		{sha: "sha1", ref: "refs/heads/main"},
		{sha: "sha2", ref: "refs/heads/dev"},
	}
	if len(b.fetchRefs) != len(wantRefs) {
		t.Fatalf("fetch refs = %v, want %v", b.fetchRefs, wantRefs)
	}
	for i, r := range b.fetchRefs {
		if r != wantRefs[i] {
			t.Errorf("fetch refs[%d] = %v, want %v", i, r, wantRefs[i])
		}
	}
	// Output ends with the batch terminator blank line.
	if !strings.HasSuffix(out.String(), "\n\n") {
		t.Errorf("output = %q, want suffix \\n\\n (batch terminator)", out.String())
	}
}

// TestPushBatch checks push lines produce per-ref ok status lines.
func TestPushBatch(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{pushRes: []pushStatus{
		{remoteRef: "refs/heads/main", ok: true},
	}}
	h, out := newHelper("push refs/heads/main:refs/heads/main\n\n", b)
	if err := h.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	wantPush := []pushRef{{localRef: "refs/heads/main", remoteRef: "refs/heads/main"}}
	if len(b.pushRefs) != len(wantPush) {
		t.Fatalf("push refs = %v, want %v", b.pushRefs, wantPush)
	}
	if b.pushRefs[0] != wantPush[0] {
		t.Errorf("push refs[0] = %v, want %v", b.pushRefs[0], wantPush[0])
	}
	want := "ok refs/heads/main\n\n"
	if got := out.String(); got != want {
		t.Errorf("push output = %q, want %q", got, want)
	}
}

// TestPushErrorStatus checks a failed push ref emits an escaped error line.
func TestPushErrorStatus(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{pushRes: []pushStatus{
		{remoteRef: "refs/heads/main", ok: false, msg: "non-fast-forward"},
	}}
	h, out := newHelper("push refs/heads/main:refs/heads/main\n\n", b)
	if err := h.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "error refs/heads/main \"non-fast-forward\"\n\n"
	if got := out.String(); got != want {
		t.Errorf("push error output = %q, want %q", got, want)
	}
}

// TestPushForce checks a leading '+' on the local ref marks a force push and
// is stripped before dispatch.
func TestPushForce(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{pushRes: []pushStatus{
		{remoteRef: "refs/heads/main", ok: true},
	}}
	h, _ := newHelper("push +refs/heads/main:refs/heads/main\n\n", b)
	if err := h.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	wantPush := []pushRef{{localRef: "refs/heads/main", remoteRef: "refs/heads/main", force: true}}
	if len(b.pushRefs) != 1 || b.pushRefs[0] != wantPush[0] {
		t.Errorf("push refs = %v, want %v", b.pushRefs, wantPush)
	}
}

// TestPushDeletion checks an empty local ref requests deletion.
func TestPushDeletion(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{pushRes: []pushStatus{
		{remoteRef: "refs/heads/old", ok: true},
	}}
	h, _ := newHelper("push :refs/heads/old\n\n", b)
	if err := h.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	wantPush := []pushRef{{localRef: "", remoteRef: "refs/heads/old", deletion: true}}
	if len(b.pushRefs) != 1 || b.pushRefs[0] != wantPush[0] {
		t.Errorf("push refs = %v, want %v", b.pushRefs, wantPush)
	}
}

// TestFetchClearsPush checks a fetch line clears the push queue.
func TestFetchClearsPush(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{}
	h, _ := newHelper("push refs/heads/main:refs/heads/main\nfetch sha1 refs/heads/main\n\n", b)
	if err := h.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(b.pushRefs) != 0 {
		t.Errorf("push refs = %v, want empty (cleared by fetch)", b.pushRefs)
	}
	if len(b.fetchRefs) != 1 {
		t.Errorf("fetch refs = %v, want 1 entry", b.fetchRefs)
	}
}

// TestPushClearsFetch checks a push line clears the fetch queue.
func TestPushClearsFetch(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{pushRes: []pushStatus{}}
	h, _ := newHelper("fetch sha1 refs/heads/main\npush refs/heads/main:refs/heads/main\n\n", b)
	if err := h.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(b.fetchRefs) != 0 {
		t.Errorf("fetch refs = %v, want empty (cleared by push)", b.fetchRefs)
	}
	if len(b.pushRefs) != 1 {
		t.Errorf("push refs = %v, want 1 entry", b.pushRefs)
	}
}

// TestEmptyBatchWritesBlankLine checks an empty batch with no queued work
// still emits the terminating blank line.
func TestEmptyBatchWritesBlankLine(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{}
	h, out := newHelper("\n", b)
	if err := h.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := out.String(); got != "\n" {
		t.Errorf("empty batch output = %q, want %q", got, "\n")
	}
}

// TestUnknownCommand checks an unrecognised command aborts the helper.
func TestUnknownCommand(t *testing.T) {
	t.Parallel()

	b := &fakeBackend{}
	h, _ := newHelper("bogus command\n", b)
	if err := h.run(); err == nil {
		t.Fatal("run err = nil, want error for unknown command")
	}
}

// TestEscapeForStdout checks the git remote-helper value escaping
// (client.py escape_for_stdout): wrap in double quotes and escape
// backslash, double-quote, newline, tab, CR, and control/non-ASCII bytes.
func TestEscapeForStdout(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "hello", `"hello"`},
		{"backslash", `a\b`, `"a\\b"`},
		{"dquote", `a"b`, `"a\"b"`},
		{"newline", "a\nb", `"a\nb"`},
		{"tab", "a\tb", `"a\tb"`},
		{"cr", "a\rb", `"a\rb"`},
		{"control byte", "\x01", `"\x01"`},
		{"del", "\x7f", `"\x7f"`},
		{"high byte", "é", `"\xe9"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := escapeForStdout(tc.input); got != tc.want {
				t.Errorf("escapeForStdout(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
