// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"image"
	"testing"

	"github.com/gmlewis/go-reticulum/qr"
	"github.com/gmlewis/go-reticulum/rns"
)

// newPaperMessage builds a packed paper-method LXMF message addressed to a
// SINGLE destination, the configuration Python's as_qr expects.
func newPaperMessage(t *testing.T) *Message {
	t.Helper()
	destinationID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(destination): %v", err)
	}
	sourceID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	ts := rns.NewTransportSystem(nil)
	destination, err := rns.NewDestination(ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(destination): %v", err)
	}
	source, err := rns.NewDestination(ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	ts.Remember(nil, destination.Hash, destinationID.GetPublicKey(), nil)
	ts.Remember(nil, source.Hash, sourceID.GetPublicKey(), nil)

	m := mustTestNewMessage(t, destination, source, "hello paper content", "paper title", nil)
	m.DesiredMethod = MethodPaper
	if err := m.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if m.PaperPacked == nil {
		t.Fatal("PaperPacked not set after Pack")
	}
	return m
}

// TestAsQRGolden verifies AsQR mirrors Python's LXMessage.as_qr: it produces a
// QR image that faithfully renders qr.Encode(AsURI(false)) at ECL L with a
// 1-module quiet zone, and applies determine_transport_encryption +
// mark_paper_generated. Because Identity.encrypt uses a fresh ephemeral X25519
// key per call, paper_packed (and thus the URI) is non-deterministic, so the
// parity bar here is that the rendered matrix exactly matches the vendored
// encoder's matrix for the URI AsQR used (proving AsQR encodes the right data
// at the right geometry), plus the Python-parity side effects. Cross-process
// URI/matrix-dimension parity against Python's qrcode is covered by the
// integration test TestIntegrationAsQRPythonMatrixParity.
func TestAsQRGolden(t *testing.T) {
	t.Parallel()

	m := newPaperMessage(t)

	// Capture the URI AsQR will encode before any side effects mutate state.
	// AsURI(false) is pure (finalise=false does not call markPaperGenerated or
	// DetermineTransportEncryption), so it is safe to call here.
	wantURI, err := m.AsURI(false)
	if err != nil {
		t.Fatalf("AsURI(false) before AsQR: %v", err)
	}
	if got, want := wantURI[:len(URISchema)+3], URISchema+"://"; got != want {
		t.Fatalf("URI prefix=%q want=%q", got, want)
	}

	// Reference matrix from the vendored encoder for that exact URI at ECL L.
	refCode, err := qr.Encode(wantURI, qr.L)
	if err != nil {
		t.Fatalf("qr.Encode reference: %v", err)
	}

	callbackFired := false
	m.DeliveryCallback = func(*Message) { callbackFired = true }

	img, err := m.AsQR()
	if err != nil {
		t.Fatalf("AsQR: %v", err)
	}
	if img == nil {
		t.Fatal("AsQR returned nil image")
	}

	// Geometry: (Size + 2*border) * boxSize pixels on a side, matching
	// Python's qrcode.make(border=1, box_size=10) pixel dimensions.
	wantPixSide := (refCode.Size + 2*qrBorder) * qrBoxSize
	bounds := img.Bounds()
	if dx, dy := bounds.Dx(), bounds.Dy(); dx != wantPixSide || dy != wantPixSide {
		t.Fatalf("image bounds=%dx%d want=%dx%d (Size=%d, border=%d, boxSize=%d)",
			dx, dy, wantPixSide, wantPixSide, refCode.Size, qrBorder, qrBoxSize)
	}

	// The rendered image must faithfully reproduce qr.Encode(URI): sample the
	// center pixel of every module and compare to the reference matrix. This
	// proves AsQR encoded exactly the AsURI(false) string at ECL L (the QR
	// version/matrix is determined by the data, so a matching matrix implies
	// matching data) and rendered it with a 1-module border offset.
	gray, ok := img.(*image.Gray)
	if !ok {
		t.Fatalf("AsQR image is %T, want *image.Gray", img)
	}
	for y := 0; y < refCode.Size; y++ {
		for x := 0; x < refCode.Size; x++ {
			cx := (qrBorder+x)*qrBoxSize + qrBoxSize/2
			cy := (qrBorder+y)*qrBoxSize + qrBoxSize/2
			got := gray.GrayAt(cx, cy).Y == 0x00 // black
			want := refCode.Black(x, y)
			if got != want {
				t.Errorf("module (%d,%d): image black=%v want=%v", x, y, got, want)
			}
		}
	}

	// Quiet zone must be all white (border around the matrix).
	for y := 0; y < wantPixSide; y++ {
		for x := 0; x < wantPixSide; x++ {
			inMatrixX := x >= qrBorder*qrBoxSize && x < (qrBorder+refCode.Size)*qrBoxSize
			inMatrixY := y >= qrBorder*qrBoxSize && y < (qrBorder+refCode.Size)*qrBoxSize
			if inMatrixX && inMatrixY {
				continue
			}
			if gray.GrayAt(x, y).Y != 0xFF {
				t.Errorf("quiet-zone pixel (%d,%d) not white", x, y)
			}
		}
	}

	// determine_transport_encryption: SINGLE destination + PAPER method => EC.
	if !m.TransportEncrypted {
		t.Error("TransportEncrypted=false, want true for SINGLE paper message")
	}
	if got, want := m.TransportEncryption, EncryptionDescriptionEC; got != want {
		t.Errorf("TransportEncryption=%q want=%q", got, want)
	}

	// mark_paper_generated: state=PAPER (0x05), progress=1.0, callback fired.
	if got, want := m.State, StatePaper; got != want {
		t.Errorf("State=0x%02x want=0x%02x (StatePaper)", got, want)
	}
	if m.Progress != 1.0 {
		t.Errorf("Progress=%v want=1.0", m.Progress)
	}
	if !callbackFired {
		t.Error("DeliveryCallback not invoked by mark_paper_generated")
	}
}

// TestAsQRPacksIfNotPacked verifies AsQR packs an unpacked message before
// generating the QR, matching Python's `if not self.packed: self.pack()`.
func TestAsQRPacksIfNotPacked(t *testing.T) {
	t.Parallel()

	m := newPaperMessage(t)
	m.Packed = nil // simulate an unpacked message

	if _, err := m.AsQR(); err != nil {
		t.Fatalf("AsQR with unpacked message: %v", err)
	}
	if m.State != StatePaper {
		t.Errorf("State=0x%02x want=0x%02x", m.State, StatePaper)
	}
}

// TestAsQRErrorsOnNonPaperMethod verifies AsQR refuses a non-paper delivery
// method, matching Python's TypeError("Attempt to represent LXM with non-paper
// delivery method as QR-code").
func TestAsQRErrorsOnNonPaperMethod(t *testing.T) {
	t.Parallel()

	m := newPaperMessage(t)
	m.DesiredMethod = MethodDirect
	// PaperPacked remains set from the earlier paper pack, but the method guard
	// fires first on DesiredMethod.

	if _, err := m.AsQR(); err == nil {
		t.Fatal("AsQR with DesiredMethod=Direct: expected error, got nil")
	}
}

// TestAsQRErrorsWhenPaperPackedNil verifies AsQR errors when the paper payload
// has not been generated (DesiredMethod is PAPER but PaperPacked is nil).
func TestAsQRErrorsWhenPaperPackedNil(t *testing.T) {
	t.Parallel()

	destinationID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(destination): %v", err)
	}
	sourceID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	ts := rns.NewTransportSystem(nil)
	destination, err := rns.NewDestination(ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(destination): %v", err)
	}
	source, err := rns.NewDestination(ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	ts.Remember(nil, destination.Hash, destinationID.GetPublicKey(), nil)
	ts.Remember(nil, source.Hash, sourceID.GetPublicKey(), nil)

	m := mustTestNewMessage(t, destination, source, "content", "title", nil)
	m.DesiredMethod = MethodPaper
	// Deliberately do NOT pack, and clear any packed state so PaperPacked is
	// nil. Pack() would populate PaperPacked, so instead set Method to paper
	// without packing by forcing the guard: AsQR calls Pack() first, which
	// WOULD set PaperPacked. To exercise the nil-paper guard we must prevent
	// Pack from succeeding — point the message at a destination whose identity
	// has no public key so encryption cannot produce a payload.
	destinationIDNoKey, err := rns.NewIdentity(false, nil)
	if err != nil {
		t.Fatalf("NewIdentity(nokey): %v", err)
	}
	destNoKey, err := rns.NewDestination(ts, destinationIDNoKey, rns.DestinationOut, rns.DestinationSingle, AppName, "nokey")
	if err != nil {
		t.Fatalf("NewDestination(nokey): %v", err)
	}
	m2 := mustTestNewMessage(t, destNoKey, source, "content", "title", nil)
	m2.DesiredMethod = MethodPaper

	if _, err := m2.AsQR(); err == nil {
		t.Fatal("AsQR with unset PaperPacked: expected error, got nil")
	}
}

// TestAsURIFinaliseMarksPaperGenerated verifies AsURI(finalise=true) applies
// determine_transport_encryption + mark_paper_generated, matching Python's
// as_uri finalise branch (LXMessage.py:706-711).
func TestAsURIFinaliseMarksPaperGenerated(t *testing.T) {
	t.Parallel()

	m := newPaperMessage(t)
	fired := false
	m.DeliveryCallback = func(*Message) { fired = true }

	if _, err := m.AsURI(true); err != nil {
		t.Fatalf("AsURI(true): %v", err)
	}
	if got, want := m.State, StatePaper; got != want {
		t.Errorf("State=0x%02x want=0x%02x", got, want)
	}
	if m.Progress != 1.0 {
		t.Errorf("Progress=%v want=1.0", m.Progress)
	}
	if !m.TransportEncrypted {
		t.Error("TransportEncrypted=false after AsURI(true) finalise")
	}
	if !fired {
		t.Error("DeliveryCallback not invoked by AsURI(true) finalise")
	}
}
