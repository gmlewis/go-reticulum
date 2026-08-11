// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"sync"
	"time"
)

const (
	// RequestFailed indicates that the request was not delivered or no response was received within the timeout.
	RequestFailed = 0x00
	// RequestSent indicates that the request has been dispatched to the network interface.
	RequestSent = 0x01
	// RequestDelivered indicates that the request reached the destination and is pending a response.
	RequestDelivered = 0x02
	// RequestReceiving indicates that the response data is currently being downloaded or assembled.
	RequestReceiving = 0x03
	// RequestReady indicates that the complete response has been received and is available for consumption.
	RequestReady = 0x04
)

// Request response codes
const (
	// ReqFetchNotAllowed indicates that fetching the requested file was not allowed by the remote.
	ReqFetchNotAllowed int64 = 0xF0
)

// RequestReceipt represents a receipt for a sent request.
type RequestReceipt struct {
	logger        *Logger
	Link          *Link
	RequestID     []byte
	PacketReceipt *PacketReceipt
	Resource      *Resource

	Response any
	// Metadata carries optional response metadata, such as resource metadata
	// attached by the remote peer.
	Metadata    any
	Status      int
	SentAt      time.Time
	StartedAt   time.Time
	ConcludedAt time.Time
	Timeout     time.Duration
	// maxResponseSize is the maximum accepted response size in bytes,
	// mirroring Python RequestReceipt.max_response_size (Link.py:1348). Zero
	// means "unlimited" (Python None); a positive value caps both inline
	// (ContextResponse) responses and response-carrying resource
	// advertisements (Link.py:862-876, 1041-1046), rejecting the receipt via
	// ResponseRejected when exceeded.
	maxResponseSize int64

	// responseSize is the uncompressed size of the received response,
	// mirroring Python RequestReceipt.response_size (Link.py:1341). It is
	// nil-unset (responseSizeSet false, Python None) until recorded. For an
	// inline (ContextResponse) response it is set in handleResponse with
	// updateSizes=true (Link.py:868); for a response resource it is set at
	// advertisement-accept time (Link.py:1050-1051), only if still unset.
	responseSize    int64
	responseSizeSet bool

	// responseTransferSize is the on-wire (possibly compressed) transfer size
	// accumulated for the received response, mirroring Python
	// RequestReceipt.response_transfer_size (Link.py:1340). It is nil-unset
	// (responseTransferSizeSet false, Python None) until the first
	// accumulation, after which it accumulates. Accumulation is gated to the
	// response-data path (handleResponse with updateSizes=true, Link.py:869-
	// 870) and the response-resource advertisement-accept path (Link.py:1052-
	// 1053); the resource-conclude path does not accumulate
	// (updateSizes=false).
	responseTransferSize    int64
	responseTransferSizeSet bool

	callback         func(*RequestReceipt)
	failedCallback   func(*RequestReceipt)
	progressCallback func(*RequestReceipt)

	mu sync.Mutex
}

// RequestReceiptCallbacks holds callbacks for request events.
type RequestReceiptCallbacks struct {
	Response func(*RequestReceipt)
	Failed   func(*RequestReceipt)
	Progress func(*RequestReceipt)
}

// GetStatus returns the current status of the request receipt.
func (rr *RequestReceipt) GetStatus() int {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return rr.Status
}

// MaxResponseSize returns the maximum accepted response size in bytes, or zero
// meaning "unlimited" (Python RequestReceipt.max_response_size, None when
// unset).
func (rr *RequestReceipt) MaxResponseSize() int64 {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return rr.maxResponseSize
}

// SetMaxResponseSize sets the maximum accepted response size in bytes. Zero
// means "unlimited". It is the transport-facing setter used by Link.Request to
// thread the caller-supplied limit onto the receipt.
func (rr *RequestReceipt) SetMaxResponseSize(max int64) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rr.maxResponseSize = max
}

// ResponseSize returns the uncompressed size of the received response, or nil
// when no response size has been recorded yet (Python
// RequestReceipt.response_size, None before any response data arrives). It is
// the Go port of Python's response_size attribute (Link.py:1341,867-868):
// set by the inline response path (handleResponse updateSizes=true) and by
// the response-resource advertisement-accept path (Link.py:1050-1051).
func (rr *RequestReceipt) ResponseSize() *int64 {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if !rr.responseSizeSet {
		return nil
	}
	v := rr.responseSize
	return &v
}

// ResponseTransferSize returns the accumulated on-wire transfer size of the
// received response, or nil when no transfer size has been recorded yet
// (Python RequestReceipt.response_transfer_size, None until the first
// accumulation). It is the Go port of Python's response_transfer_size attribute
// (Link.py:1340,869-870): accumulated by the inline response path
// (handleResponse updateSizes=true) and the response-resource
// advertisement-accept path (Link.py:1052-1053).
func (rr *RequestReceipt) ResponseTransferSize() *int64 {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if !rr.responseTransferSizeSet {
		return nil
	}
	v := rr.responseTransferSize
	return &v
}

// recordResponseSize records the response size and accumulates the transfer
// size for the response-data (inline ContextResponse) path. It is the Go port
// of Python handle_response's update_sizes block (Link.py:867-870):
// response_size is set (overwriting any prior value, matching the inline
// path's unconditional assignment), and response_transfer_size is accumulated
// (initialised from unset to 0 on first use, matching Python's
// `if None: 0` guard). The caller is handleResponse with updateSizes=true.
func (rr *RequestReceipt) recordResponseSize(responseSize, responseTransferSize int64) {
	rr.mu.Lock()
	rr.responseSize = responseSize
	rr.responseSizeSet = true
	if !rr.responseTransferSizeSet {
		rr.responseTransferSize = 0
		rr.responseTransferSizeSet = true
	}
	rr.responseTransferSize += responseTransferSize
	rr.mu.Unlock()
}

// recordResponseResourceSize records the response size from a response-resource
// advertisement at accept time (Link.py:1050-1054). response_size is set only
// if still unset (idempotent across re-advertisements, matching Python's
// `if response_size == None` guard), and response_transfer_size accumulates
// the advertised transfer size. This is the resource-path counterpart to
// recordResponseSize: it runs directly in the advertisement-accept branch,
// not through handleResponse (which uses updateSizes=false for the resource
// conclude path).
func (rr *RequestReceipt) recordResponseResourceSize(readSize, readTransferSize int64) {
	rr.mu.Lock()
	if !rr.responseSizeSet {
		rr.responseSize = readSize
		rr.responseSizeSet = true
	}
	if !rr.responseTransferSizeSet {
		rr.responseTransferSize = 0
		rr.responseTransferSizeSet = true
	}
	rr.responseTransferSize += readTransferSize
	rr.mu.Unlock()
}

// ResponseRejected marks the request receipt as failed because an oversized
// response was received, and fires the failed callback. It is the Go port of
// Python RequestReceipt.response_rejected (Link.py:1402-1410), invoked from
// the inline response path (handleResponse) and the response-resource
// advertisement path when the response size exceeds maxResponseSize. It is
// terminal-guarded and idempotent — a receipt already Failed/Ready is left
// untouched — and removing the receipt from the link's pending list is a
// no-op when the caller (handleResponse) already removed it.
func (rr *RequestReceipt) ResponseRejected() {
	rr.mu.Lock()
	if rr.Status == RequestFailed || rr.Status == RequestReady {
		rr.mu.Unlock()
		return
	}
	rr.Status = RequestFailed
	rr.ConcludedAt = time.Now()
	cb := rr.failedCallback
	rr.mu.Unlock()

	if rr.Link != nil {
		rr.Link.removePendingRequest(rr)
	}

	if cb != nil {
		cb(rr)
	}
}

// GetProgress returns response-transfer progress as a value from 0.0 to 1.0.
func (rr *RequestReceipt) GetProgress() float64 {
	rr.mu.Lock()
	resource := rr.Resource
	rr.mu.Unlock()
	if resource == nil {
		return 0
	}
	return resource.GetProgress()
}

func (rr *RequestReceipt) responseReceived(response, metadata any) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	// Terminal-guarded: if the receipt already failed — e.g. the response
	// resource stalled mid-assembly and requestTimedOut fired at the
	// deadline — do not revive it or double-fire the success callback. This
	// is the success-side counterpart to requestTimedOut's guard, so a race
	// between a last-instant completion and the deadline resolves to exactly
	// one of the success/failure callbacks.
	if rr.Status == RequestReady || rr.Status == RequestFailed {
		return
	}

	rr.Response = response
	rr.Metadata = metadata
	rr.Status = RequestReady
	rr.ConcludedAt = time.Now()

	if rr.callback != nil {
		go rr.callback(rr)
	}
}

func (rr *RequestReceipt) requestResourceConcluded(resource *Resource) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	if resource.status == ResourceStatusComplete {
		rr.logger.Debug("Request %v successfully sent as resource.", rr.RequestID)
		if rr.StartedAt.IsZero() {
			rr.StartedAt = time.Now()
		}
		rr.Status = RequestDelivered
		responseTimeout := time.Now().Add(rr.Timeout)
		go rr.responseTimeoutJob(responseTimeout)
	} else {
		rr.logger.Debug("Sending request %v as resource failed", rr.RequestID)
		rr.Status = RequestFailed
		rr.ConcludedAt = time.Now()

		if rr.Link != nil {
			rr.Link.removePendingRequest(rr)
		}

		if rr.failedCallback != nil {
			go rr.failedCallback(rr)
		}
	}
}

// responseTimeoutJob polls until the response timeout expires, then fails the
// request. It keeps watching through RequestSent/RequestDelivered/
// RequestReceiving: a response that arrives as a resource (common when the
// reply exceeds the link MDU) flips the status to RequestReceiving as soon as
// the first part lands, and a transfer that stalls mid-assembly must still
// hit the deadline. Only the terminal states (RequestReady/RequestFailed)
// cancel the watch. requestTimedOut is itself terminal-guarded, so a race
// between the deadline and a last-millisecond completion resolves to exactly
// one callback.
func (rr *RequestReceipt) responseTimeoutJob(deadline time.Time) {
	for {
		rr.mu.Lock()
		status := rr.Status
		rr.mu.Unlock()

		if status == RequestReady || status == RequestFailed {
			return
		}
		if time.Now().After(deadline) {
			rr.requestTimedOut()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// requestTimedOut fails a request that did not receive a complete response.
// It is the shared termination for both the response-timeout path
// (responseTimeoutJob) and the response-resource-failure path
// (Link.responseResourceConcluded's else branch): any receipt still in a
// non-terminal state (RequestSent/RequestDelivered/RequestReceiving) is moved
// to RequestFailed, dropped from the link's pending list, and reported via
// failedCallback. It is terminal-guarded and therefore idempotent — if the
// receipt already reached RequestReady or RequestFailed (e.g. the deadline
// raced a last-instant completion, or the resource-failure raced the
// timeout), this is a no-op, so exactly one of the success/failure callbacks
// fires. Lock order is rr.mu then (released) l.mu, matching
// removePendingRequest.
func (rr *RequestReceipt) requestTimedOut() {
	rr.mu.Lock()
	if rr.Status == RequestReady || rr.Status == RequestFailed {
		rr.mu.Unlock()
		return
	}
	rr.Status = RequestFailed
	rr.ConcludedAt = time.Now()
	cb := rr.failedCallback
	rr.mu.Unlock()

	if rr.Link != nil {
		rr.Link.removePendingRequest(rr)
	}

	if cb != nil {
		cb(rr)
	}
}

func (rr *RequestReceipt) responseResourceProgress(resource *Resource) {
	if resource == nil {
		return
	}
	rr.mu.Lock()

	if rr.Status == RequestFailed || rr.Status == RequestReady {
		rr.mu.Unlock()
		return
	}
	rr.Status = RequestReceiving
	var deliveryCB func(*PacketReceipt)
	var packetReceipt *PacketReceipt
	if rr.PacketReceipt != nil {
		rr.PacketReceipt.mu.Lock()
		if rr.PacketReceipt.Status != ReceiptDelivered {
			rr.PacketReceipt.Status = ReceiptDelivered
			rr.PacketReceipt.Proved = true
			rr.PacketReceipt.ConcludedAt = float64(time.Now().UnixNano()) / 1e9
			deliveryCB = rr.PacketReceipt.deliveryCallback
			packetReceipt = rr.PacketReceipt
		}
		rr.PacketReceipt.mu.Unlock()
	}
	cb := rr.progressCallback
	rr.mu.Unlock()

	if deliveryCB != nil {
		deliveryCB(packetReceipt)
	}
	if cb != nil {
		cb(rr)
	}
}

// GetResponse returns the response data associated with this
// receipt, or nil if no response has been received. It is the Go
// port of Python's RequestReceipt.get_response().
func (rr *RequestReceipt) GetResponse() []byte {
	if rr == nil {
		return nil
	}
	if data, ok := rr.Response.([]byte); ok {
		return data
	}
	return nil
}

// GetResponseTime returns the timestamp at which the response was
// concluded, or a zero time if no response has been received. It
// is the Go port of Python's RequestReceipt.get_response_time().
func (rr *RequestReceipt) GetResponseTime() time.Time {
	if rr == nil {
		return time.Time{}
	}
	return rr.ConcludedAt
}

// Concluded reports whether the request has been concluded (either
// with a response or a timeout). It is the Go port of Python's
// RequestReceipt.concluded().
func (rr *RequestReceipt) Concluded() bool {
	if rr == nil {
		return false
	}
	return !rr.ConcludedAt.IsZero()
}
