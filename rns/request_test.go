// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

func packedBinaryKeyMapResponse(requestID, key []byte) []byte {
	result := []byte{0x92, 0xc4, byte(len(requestID))}
	result = append(result, requestID...)
	result = append(result, 0x81, 0xc4, byte(len(key)))
	result = append(result, key...)
	result = append(result, 0x01)
	return result
}

func TestRequestResponse(t *testing.T) {
	t.Parallel()
	tsInitiator := newTestTransportSystem(t)
	tsReceiver := newTestTransportSystem(t)

	pipeInitiator, pipeReceiver, cleanup := newTestPipes(t, tsInitiator, tsReceiver)
	defer cleanup()
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "receiver")

	receiverDest.RegisterRequestHandler("/test/path", func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
		return "response data: " + string(data)
	}, AllowAll, nil, true)

	link := mustTestNewLink(t, tsInitiator, receiverDest)

	establishedInitiator := make(chan bool, 1)
	link.callbacks.LinkEstablished = func(l *Link) {
		establishedInitiator <- true
	}

	if err := link.Establish(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-establishedInitiator:
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for link establishment")
	}

	responseReceived := make(chan string, 1)
	_, err := link.Request("/test/path", []byte("hello"), func(rr *RequestReceipt) {
		responseReceived <- rr.Response.(string)
	}, nil, nil, 0, 0)

	mustTest(t, err)

	select {
	case res := <-responseReceived:
		expected := "response data: hello"
		if res != expected {
			t.Errorf("expected %v, got %v", expected, res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for response")
	}
}

func TestRequestResponseAutoCompressPolicyInlineAndResource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		responseData []byte
	}{
		{name: "InlineResponse", responseData: []byte("small-inline-response")},
		{name: "ResourceResponse", responseData: bytes.Repeat([]byte("R"), 4096)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tsInitiator := newTestTransportSystem(t)
			tsReceiver := newTestTransportSystem(t)

			pipeInitiator, pipeReceiver, cleanup := newTestPipes(t, tsInitiator, tsReceiver)
			defer cleanup()
			tsInitiator.RegisterInterface(pipeInitiator)
			tsReceiver.RegisterInterface(pipeReceiver)

			receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "receiver")

			receiverDest.RegisterRequestHandlerWithAutoCompressLimit(
				"/test/path",
				func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
					return tc.responseData
				},
				AllowAll,
				nil,
				true,
				ResourceAutoCompressMaxSize,
			)

			link := mustTestNewLink(t, tsInitiator, receiverDest)

			establishedInitiator := make(chan bool, 1)
			link.callbacks.LinkEstablished = func(l *Link) {
				establishedInitiator <- true
			}

			if err := link.Establish(); err != nil {
				t.Fatal(err)
			}

			select {
			case <-establishedInitiator:
			case <-time.After(60 * time.Second):
				t.Fatal("Timeout waiting for link establishment")
			}

			responseReceived := make(chan []byte, 1)
			_, err := link.Request("/test/path", []byte("hello"), func(rr *RequestReceipt) {
				respBytes, ok := rr.Response.([]byte)
				if !ok {
					t.Fatalf("expected []byte response, got %T", rr.Response)
				}
				responseReceived <- respBytes
			}, nil, nil, 0, 0)

			mustTest(t, err)

			select {
			case res := <-responseReceived:
				if !bytes.Equal(res, tc.responseData) {
					t.Fatalf("response mismatch: got len=%v want len=%v", len(res), len(tc.responseData))
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Timeout waiting for response")
			}
		})
	}
}

func TestRequestResponseResourceProgressCallback(t *testing.T) {
	t.Parallel()

	tsInitiator := newTestTransportSystem(t)
	tsReceiver := newTestTransportSystem(t)

	pipeInitiator, pipeReceiver, cleanup := newTestPipes(t, tsInitiator, tsReceiver)
	defer cleanup()
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "receiver")
	receiverDest.RegisterRequestHandlerWithAutoCompressLimit(
		"/test/path",
		func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
			return bytes.Repeat([]byte("R"), 4096)
		},
		AllowAll,
		nil,
		true,
		ResourceAutoCompressMaxSize,
	)

	link := mustTestNewLink(t, tsInitiator, receiverDest)

	establishedInitiator := make(chan bool, 1)
	link.callbacks.LinkEstablished = func(l *Link) {
		establishedInitiator <- true
	}

	if err := link.Establish(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-establishedInitiator:
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for link establishment")
	}

	progressObserved := make(chan int, 1)
	responseReceived := make(chan struct{}, 1)
	_, err := link.Request(
		"/test/path",
		[]byte("hello"),
		func(rr *RequestReceipt) {
			responseReceived <- struct{}{}
		},
		nil,
		func(rr *RequestReceipt) {
			select {
			case progressObserved <- rr.GetStatus():
			default:
			}
		},
		0, 0,
	)

	mustTest(t, err)

	select {
	case status := <-progressObserved:
		if status != RequestReceiving {
			t.Fatalf("progress callback status = %v, want %v", status, RequestReceiving)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for request progress callback")
	}

	select {
	case <-responseReceived:
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for response")
	}
}

func TestRequestReceiptResponseResourceProgressParity(t *testing.T) {
	t.Parallel()

	progressCalled := 0
	deliveryCalled := 0
	rr := &RequestReceipt{
		Status: RequestDelivered,
		PacketReceipt: &PacketReceipt{
			Status: ReceiptSent,
		},
	}
	rr.PacketReceipt.SetDeliveryCallback(func(pr *PacketReceipt) {
		deliveryCalled++
	})
	rr.progressCallback = func(got *RequestReceipt) {
		progressCalled++
		if got != rr {
			t.Fatalf("progress callback receipt = %p, want %p", got, rr)
		}
		if status := got.GetStatus(); status != RequestReceiving {
			t.Fatalf("progress callback status = %v, want %v", status, RequestReceiving)
		}
	}

	rr.responseResourceProgress(&Resource{})

	if got, want := progressCalled, 1; got != want {
		t.Fatalf("progress callback calls = %v, want %v", got, want)
	}
	if got, want := deliveryCalled, 1; got != want {
		t.Fatalf("delivery callback calls = %v, want %v", got, want)
	}
	if got, want := rr.GetStatus(), RequestReceiving; got != want {
		t.Fatalf("request status = %v, want %v", got, want)
	}
	if rr.PacketReceipt.ConcludedAt == 0 {
		t.Fatal("packet receipt ConcludedAt was not set")
	}
	if got, want := rr.PacketReceipt.Status, ReceiptDelivered; got != want {
		t.Fatalf("packet receipt status = %v, want %v", got, want)
	}

	rr.responseReceived([]byte("done"), nil)
	rr.responseResourceProgress(&Resource{})

	if got, want := progressCalled, 1; got != want {
		t.Fatalf("progress callback calls after ready = %v, want %v", got, want)
	}
	if got, want := rr.GetStatus(), RequestReady; got != want {
		t.Fatalf("request status after ready = %v, want %v", got, want)
	}
}

func TestRequestReceiptStoresMetadata(t *testing.T) {
	t.Parallel()

	metadata := map[string][]byte{
		"name": []byte("example.txt"),
	}
	rr := &RequestReceipt{Status: RequestDelivered}

	rr.responseReceived([]byte("payload"), metadata)

	if got, want := rr.GetStatus(), RequestReady; got != want {
		t.Fatalf("status = %v, want %v", got, want)
	}
	if got, want := rr.Response.([]byte), []byte("payload"); !bytes.Equal(got, want) {
		t.Fatalf("response = %q, want %q", got, want)
	}
	got, ok := rr.Metadata.(map[string][]byte)
	if !ok {
		t.Fatalf("metadata type = %T, want map[string][]byte", rr.Metadata)
	}
	if !bytes.Equal(got["name"], metadata["name"]) {
		t.Fatalf("metadata[name] = %q, want %q", got["name"], metadata["name"])
	}
}

func TestLinkResponseMetadata(t *testing.T) {
	t.Parallel()

	requestID := []byte("request-id")
	metadata := map[string][]byte{
		"name": []byte("inline.txt"),
	}
	rr := &RequestReceipt{RequestID: requestID, Status: RequestDelivered}
	link := &Link{
		logger:          NewLogger(),
		pendingRequests: []*RequestReceipt{rr},
	}
	link.status.Store(LinkActive)

	link.handleResponse(requestID, []byte("inline"), metadata, 0, 0, false, false)

	got, ok := rr.Metadata.(map[string][]byte)
	if !ok {
		t.Fatalf("metadata type = %T, want map[string][]byte", rr.Metadata)
	}
	if !bytes.Equal(got["name"], metadata["name"]) {
		t.Fatalf("metadata[name] = %q, want %q", got["name"], metadata["name"])
	}
	if len(link.pendingRequests) != 0 {
		t.Fatalf("pendingRequests len = %v, want 0", len(link.pendingRequests))
	}
}

func TestResourceResponseMetadata(t *testing.T) {
	t.Parallel()

	requestID := []byte("resource-request-id")
	metadata := map[string][]byte{
		"name": []byte("resource.bin"),
	}
	packedResponse, err := msgpack.Pack([]any{requestID, []byte("resource-response")})
	if err != nil {
		t.Fatalf("failed to pack response: %v", err)
	}
	rr := &RequestReceipt{RequestID: requestID, Status: RequestDelivered}
	link := &Link{
		logger:          NewLogger(),
		pendingRequests: []*RequestReceipt{rr},
	}
	link.status.Store(LinkActive)
	resource := &Resource{
		link:     link,
		status:   ResourceStatusComplete,
		data:     packedResponse,
		metadata: metadata,
	}

	link.responseResourceConcluded(resource)

	got, ok := rr.Metadata.(map[string][]byte)
	if !ok {
		t.Fatalf("metadata type = %T, want map[string][]byte", rr.Metadata)
	}
	if !bytes.Equal(got["name"], metadata["name"]) {
		t.Fatalf("metadata[name] = %q, want %q", got["name"], metadata["name"])
	}
}

func TestUnpackRequestResponseDataPreservesBinaryMapKeys(t *testing.T) {
	t.Parallel()

	requestID := []byte("req")
	unpacked, err := unpackRequestResponseData(packedBinaryKeyMapResponse(requestID, []byte("ab")))
	if err != nil {
		t.Fatalf("unpackRequestResponseData: %v", err)
	}
	resList, ok := unpacked.([]any)
	if !ok {
		t.Fatalf("response type = %T, want []any", unpacked)
	}
	if got := resList[0].([]byte); !bytes.Equal(got, requestID) {
		t.Fatalf("request ID = %x, want %x", got, requestID)
	}

	got, ok := resList[1].(map[any]any)
	if !ok {
		t.Fatalf("response payload type = %T, want map[any]any", resList[1])
	}
	if len(got) != 1 {
		t.Fatalf("response entry count = %v, want 1", len(got))
	}
	for key, value := range got {
		rt := reflect.TypeOf(key)
		if rt == nil || rt.Kind() != reflect.String || rt.PkgPath() != "github.com/gmlewis/go-reticulum/rns/msgpack" || rt.Name() != "binaryMapKey" {
			t.Fatalf("response key type = %T (%v %v), want msgpack.binaryMapKey", key, rt.PkgPath(), rt.Name())
		}
		if gotKey := []byte(reflect.ValueOf(key).String()); !bytes.Equal(gotKey, []byte("ab")) {
			t.Fatalf("response key bytes = %x, want %x", gotKey, []byte("ab"))
		}
		if value != int64(1) {
			t.Fatalf("response value = %#v, want int64(1)", value)
		}
	}
}

func TestLinkResponseResourceConcludedPreservesBinaryMapKeys(t *testing.T) {
	t.Parallel()

	requestID := []byte("req")
	rr := &RequestReceipt{RequestID: requestID, Status: RequestDelivered}
	link := &Link{
		logger:          NewLogger(),
		pendingRequests: []*RequestReceipt{rr},
	}
	link.status.Store(LinkActive)
	resource := &Resource{
		link:   link,
		status: ResourceStatusComplete,
		data:   packedBinaryKeyMapResponse(requestID, []byte("ab")),
	}

	link.responseResourceConcluded(resource)

	got, ok := rr.Response.(map[any]any)
	if !ok {
		t.Fatalf("response type = %T, want map[any]any", rr.Response)
	}
	if len(got) != 1 {
		t.Fatalf("response entry count = %v, want 1", len(got))
	}
	for key, value := range got {
		rt := reflect.TypeOf(key)
		if rt == nil || rt.Kind() != reflect.String || rt.PkgPath() != "github.com/gmlewis/go-reticulum/rns/msgpack" || rt.Name() != "binaryMapKey" {
			t.Fatalf("response key type = %T (%v %v), want msgpack.binaryMapKey", key, rt.PkgPath(), rt.Name())
		}
		if gotKey := []byte(reflect.ValueOf(key).String()); !bytes.Equal(gotKey, []byte("ab")) {
			t.Fatalf("response key bytes = %x, want %x", gotKey, []byte("ab"))
		}
		if value != int64(1) {
			t.Fatalf("response value = %#v, want int64(1)", value)
		}
	}
}

// TestResponseResourceFailedFailsPendingReceipt covers Gap B: when a response
// resource concludes FAILED (watchdog part-timeout, exhausted retries, bad
// proof), the pending request receipt must be moved to RequestFailed, dropped
// from the link's pending list, and reported via failedCallback. Before the
// fix responseResourceConcluded only handled ResourceStatusComplete, so a
// failed response resource was silently ignored and the receipt leaked
// (neither callback fired; the caller only resolved via its own backstop).
func TestResponseResourceFailedFailsPendingReceipt(t *testing.T) {
	t.Parallel()

	requestID := []byte("failed-response-req")
	rr := &RequestReceipt{RequestID: requestID, Status: RequestReceiving}
	link := &Link{
		logger:          NewLogger(),
		pendingRequests: []*RequestReceipt{rr},
	}
	link.status.Store(LinkActive)
	rr.Link = link

	failed := make(chan *RequestReceipt, 1)
	rr.failedCallback = func(got *RequestReceipt) { failed <- got }

	resource := &Resource{
		link:      link,
		status:    ResourceStatusFailed,
		requestID: requestID,
	}

	link.responseResourceConcluded(resource)

	select {
	case got := <-failed:
		if got != rr {
			t.Fatalf("failedCallback receipt = %p, want %p", got, rr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("failedCallback never fired for failed response resource")
	}

	if got, want := rr.GetStatus(), RequestFailed; got != want {
		t.Fatalf("status = %v, want %v", got, want)
	}
	if len(link.pendingRequests) != 0 {
		t.Fatalf("pendingRequests len = %v, want 0 (receipt should be removed)", len(link.pendingRequests))
	}
}

// TestResponseTimeoutJobDisarmsOnReceivingState covers the fix for the root
// cause of "Request timed out" on multi-hop paths: a response that arrives as
// a resource flips the receipt to RequestReceiving as soon as the first part
// lands. Python's __response_timeout_job (Link.py:1383-1389) runs ONLY while
// status == DELIVERED and exits immediately when status changes to RECEIVING,
// disarming the response timeout — the resource's own watchdog then handles
// the rest of the transfer. The Go port previously kept ticking through
// RequestReceiving and fired at the fixed deadline, killing in-progress
// multi-hop resource transfers. This test verifies the corrected behavior:
// the timeout does NOT fire from RequestReceiving state.
func TestResponseTimeoutJobFiresFromReceivingState(t *testing.T) {
	t.Parallel()

	rr := &RequestReceipt{Status: RequestReceiving}
	failed := make(chan *RequestReceipt, 1)
	rr.failedCallback = func(got *RequestReceipt) { failed <- got }

	// Deadline in the past: with the old (buggy) code the first poll
	// iteration would fire immediately. With the fix, the loop exits
	// immediately because status != RequestDelivered, so the failed
	// callback must NOT fire.
	go rr.responseTimeoutJob(time.Now().Add(-1 * time.Second))

	select {
	case <-failed:
		t.Fatal("responseTimeoutJob fired from RequestReceiving state; it should disarm (exit without firing) when status is not RequestDelivered, matching Python's while self.status == DELIVERED loop")
	case <-time.After(300 * time.Millisecond):
		// Good: the timeout job exited without firing.
	}

	if got, want := rr.GetStatus(), RequestReceiving; got != want {
		t.Fatalf("status = %v, want %v (should be unchanged — timeout must not fire from receiving state)", got, want)
	}
}

// TestResponseTimeoutJobFiresFromDeliveredState verifies that the response
// timeout DOES fire when the receipt is in RequestDelivered state and the
// deadline has passed — i.e. the response never arrived. This is the correct
// behavior matching Python's __response_timeout_job which runs while
// status == DELIVERED and fires request_timed_out at the deadline.
func TestResponseTimeoutJobFiresFromDeliveredState(t *testing.T) {
	t.Parallel()

	rr := &RequestReceipt{Status: RequestDelivered}
	failed := make(chan *RequestReceipt, 1)
	rr.failedCallback = func(got *RequestReceipt) { failed <- got }

	// Deadline in the past: the first poll iteration should fire immediately.
	go rr.responseTimeoutJob(time.Now().Add(-1 * time.Second))

	select {
	case <-failed:
		// Good: the timeout fired.
	case <-time.After(2 * time.Second):
		t.Fatal("responseTimeoutJob did not fire from RequestDelivered state")
	}

	if got, want := rr.GetStatus(), RequestFailed; got != want {
		t.Fatalf("status = %v, want %v", got, want)
	}
}

// TestResponseTimeoutJobDisarmsOnStatusChange verifies the key fix: the
// timeout job exits (disarms) when the status changes from RequestDelivered
// to RequestReceiving mid-wait, matching Python's
// while self.status == DELIVERED loop exit on status change.
func TestResponseTimeoutJobDisarmsOnStatusChange(t *testing.T) {
	t.Parallel()

	rr := &RequestReceipt{Status: RequestDelivered}
	failed := make(chan *RequestReceipt, 1)
	rr.failedCallback = func(got *RequestReceipt) { failed <- got }

	// Long deadline — the timeout should NOT fire before we change the status.
	go rr.responseTimeoutJob(time.Now().Add(10 * time.Second))

	// After a short wait, flip status to RequestReceiving (simulating the
	// first part of a response resource arriving).
	time.Sleep(200 * time.Millisecond)
	rr.mu.Lock()
	rr.Status = RequestReceiving
	rr.mu.Unlock()

	select {
	case <-failed:
		t.Fatal("responseTimeoutJob fired after status changed to RequestReceiving; it should have disarmed")
	case <-time.After(500 * time.Millisecond):
		// Good: the timeout job exited without firing.
	}

	if got, want := rr.GetStatus(), RequestReceiving; got != want {
		t.Fatalf("status = %v, want %v", got, want)
	}
}

// TestResponseReceivedDoesNotReviveFailedReceipt covers the terminal guard
// added to responseReceived: a receipt already failed (e.g. by the timeout
// job) must not be revived to RequestReady or double-fire the success
// callback when a late completion races in.
func TestResponseReceivedDoesNotReviveFailedReceipt(t *testing.T) {
	t.Parallel()

	rr := &RequestReceipt{Status: RequestFailed}
	calls := 0
	rr.callback = func(*RequestReceipt) { calls++ }

	rr.responseReceived([]byte("late"), nil)

	if got, want := rr.GetStatus(), RequestFailed; got != want {
		t.Fatalf("status = %v, want %v (failed receipt revived)", got, want)
	}
	if got, want := calls, 0; got != want {
		t.Fatalf("success callback calls = %v, want %v (double-fire)", got, want)
	}
}

func TestRequestReceiptConvenience(t *testing.T) {
	t.Parallel()

	rr := &RequestReceipt{}
	now := time.Now()

	// Initially, no response, no concluded.
	if got := rr.GetResponse(); got != nil {
		t.Fatalf("GetResponse() = %v, want nil for fresh receipt", got)
	}
	if got := rr.GetResponseTime(); !got.IsZero() {
		t.Fatalf("GetResponseTime() = %v, want zero for fresh receipt", got)
	}

	// Set response data and ConcludedAt, then verify the accessors.
	rr.Response = []byte("response-data")
	rr.ConcludedAt = now

	if got := rr.GetResponse(); !bytes.Equal(got, []byte("response-data")) {
		t.Fatalf("GetResponse() = %v, want response-data", got)
	}
	if got := rr.GetResponseTime(); !got.Equal(now) {
		t.Fatalf("GetResponseTime() = %v, want %v", got, now)
	}

	// Concluded reports true when ConcludedAt is set.
	if !rr.Concluded() {
		t.Fatal("Concluded() should be true after ConcludedAt is set")
	}

	rr2 := &RequestReceipt{}
	if rr2.Concluded() {
		t.Fatal("Concluded() should be false for fresh receipt")
	}
}
