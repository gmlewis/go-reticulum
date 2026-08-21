// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
	"time"
)

// TestRequestReceiptResponseSizeRecordedExactlyOnceInline asserts that
// receiving an inline (ContextResponse) response records the response size
// and transfer size exactly once on the receipt (Link.py:867-870,
// 1009-1010). The inline response path calls handleResponse with
// update_sizes=True, which sets response_size and accumulates
// response_transfer_size once; the conclude path is not involved for inline
// responses, so the accumulator fires exactly once and the recorded transfer
// size equals the response size (both are transfer_size =
// len(packb(response_data))-2).
func TestRequestReceiptResponseSizeRecordedExactlyOnceInline(t *testing.T) {
	t.Parallel()
	// 100-byte response => packed response ~137 bytes < mdu~431, so it stays
	// inline. transfer_size = len(packb(100-byte []byte))-2 = 100.
	responsePayload := bytes.Repeat([]byte{0x01}, 100)
	initiator, _, _ := establishMaxResponseLinkPair(t, responsePayload)

	response, failed := sendMaxResponseRequest(t, initiator, 0)

	var rr *RequestReceipt
	select {
	case rr = <-response:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: response callback did not fire")
	}
	select {
	case <-failed:
		t.Fatal("failed callback fired for an accepted inline response")
	default:
	}

	// response_size must be recorded (non-nil) and equal the on-wire size
	// (transfer_size = len(packb(response))-2 = 100 for a 100-byte bin).
	gotSize := rr.ResponseSize()
	if gotSize == nil {
		t.Fatal("ResponseSize = nil after receiving an inline response; want it recorded")
	}
	if *gotSize != 100 {
		t.Errorf("ResponseSize = %v, want 100 (transfer_size of a 100-byte response)", *gotSize)
	}

	// response_transfer_size must be recorded exactly once: nil would mean
	// never accumulated, 2*transfer_size would mean accumulated twice. The
	// inline path calls handleResponse once with update_sizes=True, so it
	// must equal the response size (100), not 0 and not 200.
	gotTransfer := rr.ResponseTransferSize()
	if gotTransfer == nil {
		t.Fatal("ResponseTransferSize = nil after receiving an inline response; want it accumulated")
	}
	if *gotTransfer != 100 {
		t.Errorf("ResponseTransferSize = %v, want 100 (accumulated exactly once)", *gotTransfer)
	}
}

// TestRequestReceiptResponseSizeUnsetBeforeResponse asserts the pre-response
// state: before any response arrives, both ResponseSize and
// ResponseTransferSize are unset (nil, Python None) — the size fields are
// only populated by the response-data path.
//
// The request is sent to a path with no registered handler, so the receiver
// never generates a response (Link.handleRequest returns without sending
// when no handler matches). This keeps the receipt deterministically in the
// pending/pre-response state: the size fields can only be set by the
// response-data path (handleResponse -> recordResponseSize), which never runs.
// Requesting "/p" against the echo handler would race the assertion against a
// fast loopback response that records the sizes (observed as a flaky CI
// failure where ResponseSize came back 100 instead of nil).
func TestRequestReceiptResponseSizeUnsetBeforeResponse(t *testing.T) {
	t.Parallel()
	initiator, _, _ := establishMaxResponseLinkPair(t, bytes.Repeat([]byte{0x01}, 100))

	// Issue a request to an unregistered path; inspect the receipt
	// immediately while it is still pending and no response will arrive.
	rr, err := initiator.Request("/no-such-handler", bytes.Repeat([]byte{0x00}, 10), nil, nil, nil, 30*time.Second, 0)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if got := rr.ResponseSize(); got != nil {
		t.Errorf("ResponseSize before any response = %v, want nil", *got)
	}
	if got := rr.ResponseTransferSize(); got != nil {
		t.Errorf("ResponseTransferSize before any response = %v, want nil", *got)
	}
}

// TestRequestReceiptResponseSizeRecordedExactlyOnceResource asserts the
// response-resource path: the response size and transfer size
// are recorded exactly once, at advertisement-accept time (Link.py:1049-1054),
// and the conclude path (update_sizes=False) does not re-accumulate them. A
// 500-byte response packs to ~538 bytes > mdu~431, so it is sent as a
// resource; adv.D (read_size = uncompressed data size ~538) is recorded as
// response_size, and adv.T (read_transfer_size) is accumulated once as
// response_transfer_size.
func TestRequestReceiptResponseSizeRecordedExactlyOnceResource(t *testing.T) {
	t.Parallel()
	responsePayload := bytes.Repeat([]byte{0x02}, 500)
	initiator, _, _ := establishMaxResponseLinkPair(t, responsePayload)

	response, failed := sendMaxResponseRequest(t, initiator, 0)

	var rr *RequestReceipt
	select {
	case rr = <-response:
	case <-time.After(30 * time.Second):
		t.Fatal("timeout: response callback did not fire for the response resource")
	}
	select {
	case <-failed:
		t.Fatal("failed callback fired for an accepted response resource")
	default:
	}

	// Both size fields must be recorded (non-nil): the resource-path sizes
	// are set at accept time, not at conclude. response_size = adv.D (the
	// uncompressed data size of the packed response); it must be > 500 (the
	// response payload alone is 500 bytes, plus the request-id and array
	// framing). The conclude path passes updateSizes=False, so it does not
	// overwrite response_size with its (0) argument — the non-zero value
	// here proves the conclude path's guard works.
	gotSize := rr.ResponseSize()
	if gotSize == nil {
		t.Fatal("ResponseSize = nil after receiving a response resource; want it recorded at accept time")
	}
	if *gotSize <= 500 {
		t.Errorf("ResponseSize = %v, want > 500 (uncompressed packed-response size)", *gotSize)
	}

	// response_transfer_size = adv.T (the on-wire transfer size), accumulated
	// exactly once at accept. With AutoCompress disabled the transfer size
	// still exceeds the data size because it includes per-part framing
	// overhead (resource.size vs resource.total_size), so assert it is
	// recorded, positive, and at least the data size — but not double the
	// data size (which would indicate the accept path or the conclude path
	// accumulated twice).
	gotTransfer := rr.ResponseTransferSize()
	if gotTransfer == nil {
		t.Fatal("ResponseTransferSize = nil after receiving a response resource; want it accumulated at accept time")
	}
	if *gotTransfer < *gotSize {
		t.Errorf("ResponseTransferSize = %v, want >= %v (on-wire transfer size includes the data)", *gotTransfer, *gotSize)
	}
	if *gotTransfer >= 2**gotSize {
		t.Errorf("ResponseTransferSize = %v, want < 2*%v (accumulated exactly once, not doubled)", *gotTransfer, *gotSize)
	}
}
