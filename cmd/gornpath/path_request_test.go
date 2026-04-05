// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

type pathRequestFake struct {
	path      *rns.PathInfo
	revealAt  int
	requested int
	reqErr    error
	checks    int
}

func (f *pathRequestFake) HasPath([]byte) bool {
	f.checks++
	if f.path == nil && f.revealAt > 0 && f.checks >= f.revealAt {
		f.path = &rns.PathInfo{Hash: []byte{0xaa, 0xbb}, NextHop: []byte{0xcc, 0xdd}, Hops: 2, Interface: pathRequestInterface{}, Expires: time.Unix(0, 0)}
	}
	return f.path != nil
}

func (f *pathRequestFake) RequestPath([]byte) error {
	f.requested++
	return f.reqErr
}

func (f *pathRequestFake) GetPathEntry([]byte) *rns.PathInfo { return f.path }

type pathRequestClock struct {
	now time.Time
}

func (c *pathRequestClock) Now() time.Time        { return c.now }
func (c *pathRequestClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }

type pathRequestInterface struct{}

func (pathRequestInterface) Name() string          { return "eth0" }
func (pathRequestInterface) Type() string          { return "test" }
func (pathRequestInterface) Status() bool          { return true }
func (pathRequestInterface) IsOut() bool           { return false }
func (pathRequestInterface) Mode() int             { return interfaces.ModeFull }
func (pathRequestInterface) Bitrate() int          { return 0 }
func (pathRequestInterface) Send([]byte) error     { return nil }
func (pathRequestInterface) BytesReceived() uint64 { return 0 }
func (pathRequestInterface) BytesSent() uint64     { return 0 }
func (pathRequestInterface) Detach() error         { return nil }
func (pathRequestInterface) IsDetached() bool      { return false }
func (pathRequestInterface) Age() time.Duration    { return 0 }

func TestDoRequestPrintsSpinnerAndSuccessMessage(t *testing.T) {
	t.Parallel()

	clock := &pathRequestClock{now: time.Unix(0, 0)}
	fake := &pathRequestFake{revealAt: 2}
	var out bytes.Buffer
	if err := doRequestAt(&out, fake, []byte{0xaa, 0xbb}, 1.0, clock.Now, clock.Sleep); err != nil {
		t.Fatalf("doRequestAt returned error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Path to aabb requested") {
		t.Fatalf("missing request message: %q", got)
	}
	if !strings.Contains(got, "Path found, destination aabb is 2 hops away via ccdd on eth0") {
		t.Fatalf("missing success message: %q", got)
	}
	if fake.requested != 1 {
		t.Fatalf("expected one path request, got %v", fake.requested)
	}
}

func TestDoRequestTimesOutWithClearMessage(t *testing.T) {
	t.Parallel()

	clock := &pathRequestClock{now: time.Unix(0, 0)}
	fake := &pathRequestFake{}
	var out bytes.Buffer
	err := doRequestAt(&out, fake, []byte{0xaa, 0xbb}, 0.1, clock.Now, clock.Sleep)
	if err == nil || err.Error() != "Path not found" {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "Path not found") {
		t.Fatalf("missing timeout message: %q", out.String())
	}
}

func TestDoRequestReturnsRequestError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	fake := &pathRequestFake{reqErr: wantErr}
	var out bytes.Buffer
	err := doRequestAt(&out, fake, []byte{0xaa, 0xbb}, 1.0, func() time.Time { return time.Unix(0, 0) }, func(time.Duration) {})
	if err == nil || !strings.Contains(err.Error(), "Could not request path to aabb") {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.requested != 1 {
		t.Fatalf("expected one request attempt, got %v", fake.requested)
	}
}
