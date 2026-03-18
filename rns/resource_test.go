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
)

type errReader struct{}

var randReaderMu sync.Mutex

func (errReader) Read(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestNewResourceFailsOnRandomHashGenerationError(t *testing.T) {
	randReaderMu.Lock()
	defer randReaderMu.Unlock()

	key := make([]byte, 32)
	token, err := crypto.NewToken(key)
	if err != nil {
		t.Fatalf("NewToken error: %v", err)
	}

	link := &Link{
		status: LinkActive,
		token:  token,
		mdu:    MDU,
	}

	originalRandRead := resourceRandRead
	resourceRandRead = func(p []byte) (int, error) {
		return errReader{}.Read(p)
	}
	defer func() { resourceRandRead = originalRandRead }()

	_, err = NewResource([]byte("payload"), link)
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

	return &Link{
		status: LinkActive,
		token:  token,
		mdu:    MDU,
	}
}

func TestNewResourceWithOptionsCompressesWhenSmaller(t *testing.T) {
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
	link := testActiveResourceLink(t)
	data := bytes.Repeat([]byte("A"), 2048)

	r := mustTestNewResourceWithOptions(t, data, link, ResourceOptions{AutoCompress: true, AutoCompressLimit: 64})
	if r.compressed {
		t.Fatalf("expected uncompressed resource when payload exceeds auto-compress limit")
	}
}

func TestResourceStatusAndDataAccessors(t *testing.T) {
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
