// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/crypto"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

type errReader struct{}

var randReaderMu sync.Mutex

func (errReader) Read(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestNewResourceFailsOnRandomHashGenerationError(t *testing.T) {
	t.Parallel()
	randReaderMu.Lock()
	defer randReaderMu.Unlock()

	key := make([]byte, 32)
	token, err := crypto.NewToken(key)
	if err != nil {
		t.Fatalf("NewToken error: %v", err)
	}

	link := &Link{
		token: token,
		mdu:   MDU,
	}
	link.status.Store(LinkActive)

	_, err = newResourceWithOptions([]byte("payload"), link, ResourceOptions{}, errReader{}.Read)
	if err == nil {
		t.Fatalf("expected random hash generation error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to generate random hash") {
		t.Fatalf("expected random hash error message, got %v", err)
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected wrapped io.ErrUnexpectedEOF, got %v", err)
	}
}

func testActiveResourceLink(t *testing.T) *Link {
	t.Helper()

	key := make([]byte, 32)
	token, err := crypto.NewToken(key)
	if err != nil {
		t.Fatalf("NewToken error: %v", err)
	}

	link := &Link{
		token: token,
		mdu:   MDU,
	}
	link.status.Store(LinkActive)
	return link
}

func TestNewResourceWithOptionsCompressesWhenSmaller(t *testing.T) {
	t.Parallel()
	link := testActiveResourceLink(t)
	data := bytes.Repeat([]byte("AAAAAAAAAAAAAAAA"), 1024)

	r := mustTestNewResourceWithOptions(t, data, link, ResourceOptions{AutoCompress: true})
	if !r.compressed {
		t.Fatalf("expected compressed resource for highly repetitive payload")
	}

	plaintext, err := link.Decrypt(r.data)
	if err != nil {
		t.Fatalf("Decrypt error: %v", err)
	}
	if len(plaintext) <= ResourceRandomHashSize {
		t.Fatalf("resource plaintext too small")
	}
	decompressed, err := DecompressBzip2(plaintext[ResourceRandomHashSize:])
	if err != nil {
		t.Fatalf("DecompressBzip2 error: %v", err)
	}
	if !bytes.Equal(decompressed, data) {
		t.Fatalf("decompressed payload mismatch")
	}
}

func TestNewResourceWithOptionsRespectsCompressionLimit(t *testing.T) {
	t.Parallel()
	link := testActiveResourceLink(t)
	data := bytes.Repeat([]byte("A"), 2048)

	r := mustTestNewResourceWithOptions(t, data, link, ResourceOptions{AutoCompress: true, AutoCompressLimit: 64})
	if r.compressed {
		t.Fatalf("expected uncompressed resource when payload exceeds auto-compress limit")
	}
}

// TestResourceAssembleBz2BombGuard verifies the decompression-bomb guard in
// Resource.Assemble (Python Resource.assemble, Resource.py:690-696): a
// compressed incoming resource whose decompressed size exceeds
// maxDecompressedSize is rejected — status set to CORRUPT, the resource
// removed from the link's incoming set, and the link torn down — without
// decompressing the full bomb (no OOM). The 64 KiB bomb decompresses past the
// 1 KiB cap; a normal hash-mismatch CORRUPT does NOT tear the link down, so
// LinkClosed proves the bomb-guard branch ran.
func TestResourceAssembleBz2BombGuard(t *testing.T) {
	t.Parallel()

	link := testActiveResourceLink(t)
	link.logger = testSilentLogger()
	// linkID stays empty so Link.sendTeardownPacket is a no-op; teardown still
	// flips status to LinkClosed.

	// Build a bz2 bomb: small compressed form, 64 KiB decompressed.
	bomb := bytes.Repeat([]byte("B"), 64*1024)
	compressedBomb, err := CompressBzip2(bomb, 9)
	if err != nil {
		t.Fatalf("CompressBzip2: %v", err)
	}

	randomHash := make([]byte, ResourceRandomHashSize)
	r := &Resource{
		link:                link,
		compressed:          true,
		randomHash:          randomHash,
		hash:                bytes.Repeat([]byte{0xAB}, 32),
		parts:               []*ResourcePart{{ReceivedData: append(append([]byte(nil), randomHash...), compressedBomb...)}},
		maxDecompressedSize: 1024, // 1 KiB cap — bomb decompresses to 64 KiB
		// Deliberately invalid advertisement so Reject returns early without
		// needing a transport; the bomb-guard still runs the full CORRUPT path.
		advertisementPacket: &Packet{Data: []byte("not-an-advertisement")},
	}
	link.incomingResources = append(link.incomingResources, r)

	r.Assemble()

	if r.status != ResourceStatusCorrupt {
		t.Fatalf("resource status = %v, want ResourceStatusCorrupt", r.status)
	}
	if link.status.Load() != LinkClosed {
		t.Fatalf("link status = %v, want LinkClosed (bomb-guard tears down the link)", link.status.Load())
	}
	for _, existing := range link.incomingResources {
		if existing == r {
			t.Fatal("bomb resource was not removed from link.incomingResources")
		}
	}
}

func TestResourceStatusAndDataAccessors(t *testing.T) {
	t.Parallel()
	r := &Resource{
		status: ResourceStatusComplete,
		data:   []byte{0x01, 0x02, 0x03},
	}

	if got := r.Status(); got != ResourceStatusComplete {
		t.Fatalf("Status()=%v want=%v", got, ResourceStatusComplete)
	}

	got := r.Data()
	if len(got) != 3 || got[0] != 0x01 || got[1] != 0x02 || got[2] != 0x03 {
		t.Fatalf("Data()=%v want=[1 2 3]", got)
	}

	got[0] = 0xFF
	second := r.Data()
	if second[0] != 0x01 {
		t.Fatal("Data() must return a copy")
	}
}

func TestResourceValidateProofSuccess(t *testing.T) {
	t.Parallel()
	payload := []byte("payload")
	randomHash := []byte{0x01, 0x02, 0x03, 0x04}
	hash := FullHash(append(append([]byte{}, payload...), randomHash...))
	expectedProof := FullHash(append(append([]byte{}, payload...), hash...))

	r := &Resource{
		hash:          hash,
		expectedProof: expectedProof,
		status:        ResourceStatusAwaitingProof,
	}

	called := make(chan struct{}, 1)
	r.callback = func(*Resource) { called <- struct{}{} }

	proofData := append(append([]byte{}, hash...), expectedProof...)
	r.ValidateProof(proofData)

	if r.status != ResourceStatusComplete {
		t.Fatalf("expected status %v, got %v", ResourceStatusComplete, r.status)
	}

	select {
	case <-called:
	case <-time.After(10 * time.Second):
		t.Fatal("expected callback to be called")
	}
}

func TestResourceValidateProofFailure(t *testing.T) {
	t.Parallel()
	payload := []byte("payload")
	randomHash := []byte{0x01, 0x02, 0x03, 0x04}
	hash := FullHash(append(append([]byte{}, payload...), randomHash...))
	expectedProof := FullHash(append(append([]byte{}, payload...), hash...))

	r := &Resource{
		hash:          hash,
		expectedProof: expectedProof,
		status:        ResourceStatusAwaitingProof,
	}

	called := make(chan struct{}, 1)
	r.callback = func(*Resource) { called <- struct{}{} }

	badProof := bytes.Repeat([]byte{0xAA}, len(expectedProof))
	proofData := append(append([]byte{}, hash...), badProof...)
	r.ValidateProof(proofData)

	if r.status != ResourceStatusFailed {
		t.Fatalf("expected status %v, got %v", ResourceStatusFailed, r.status)
	}

	select {
	case <-called:
	case <-time.After(10 * time.Second):
		t.Fatal("expected callback to be called")
	}
}

func TestResourceAccessors(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(ts, id, DestinationIn, DestinationSingle, "test", "app")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	link, err := NewLink(ts, dest)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}

	res := &Resource{link: link, data: []byte("hello")}

	if got := res.GetDataSize(); got != 5 {
		t.Fatalf("GetDataSize = %d, want 5", got)
	}
	if res.IsRequest() {
		t.Fatal("IsRequest should default to false")
	}
	if res.IsResponse() {
		t.Fatal("IsResponse should default to false")
	}
	res.isResponse = true
	res.requestID = []byte("req-id-1234567890")
	if !res.IsResponse() {
		t.Fatal("IsResponse should be true after set")
	}
	if res.IsCompressed() {
		t.Fatal("IsCompressed should default to false")
	}
	// The remaining accessors are not currently populated by the
	// constructor but the contract is "no panic and reasonable
	// zero values for un-initialised fields".
	_ = res.GetParts()
	_ = res.GetSegments()
	_ = res.GetTransferSize()
}

// TestUnpackResourceAdvertisementRejectsMalformed is a regression test for two
// panic classes in UnpackResourceAdvertisement:
//   - a str-typed (rather than bin-typed) value for the h/r/o/q/m fields
//     panicked on a bare v.([]byte) type assertion;
//   - a negative or oversized part count (n) panicked makeslice / OOM-threw
//     when a caller fed adv.N into make([]*ResourcePart, n).
//
// All such malformed advertisements must now be rejected with an error.
func TestUnpackResourceAdvertisementRejectsMalformed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		m    map[string]any
	}{
		{"h is str not bytes", map[string]any{"h": "deadbeef"}},
		{"r is str not bytes", map[string]any{"r": "x"}},
		{"m is str not bytes", map[string]any{"m": "y"}},
		{"q is str not bytes", map[string]any{"q": "z"}},
		{"o is str not bytes", map[string]any{"o": "w"}},
		{"negative part count", map[string]any{"n": int64(-1)}},
		{"part count exceeds backstop", map[string]any{"n": int64(ResourceMaxParts + 1)}},
		{"part count exceeds total size", map[string]any{"t": int64(10), "n": int64(100)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, err := msgpack.Pack(tc.m)
			if err != nil {
				t.Fatalf("Pack: %v", err)
			}
			_, err = UnpackResourceAdvertisement(data)
			if err == nil {
				t.Error("expected error for malformed advertisement, got nil")
			}
		})
	}
}
