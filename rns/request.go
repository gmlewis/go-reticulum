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
	Link          *Link
	RequestID     []byte
	PacketReceipt *PacketReceipt
	Resource      *Resource

	Response    any
	Status      int
	SentAt      time.Time
	StartedAt   time.Time
	ConcludedAt time.Time
	Timeout     time.Duration

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

func (rr *RequestReceipt) responseReceived(response, metadata any) {
	// TODO: Why is metadata unused?
	rr.mu.Lock()
	defer rr.mu.Unlock()

	rr.Response = response
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
		Logf("Request %v successfully sent as resource.", LogDebug, false, rr.RequestID)
		if rr.StartedAt.IsZero() {
			rr.StartedAt = time.Now()
		}
		rr.Status = RequestDelivered
		responseTimeout := time.Now().Add(rr.Timeout)
		go rr.responseTimeoutJob(responseTimeout)
	} else {
		Logf("Sending request %v as resource failed", LogDebug, false, rr.RequestID)
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

// responseTimeoutJob polls until the response timeout expires, then fails the request.
func (rr *RequestReceipt) responseTimeoutJob(deadline time.Time) {
	for {
		rr.mu.Lock()
		status := rr.Status
		rr.mu.Unlock()

		if status != RequestDelivered {
			return
		}
		if time.Now().After(deadline) {
			rr.requestTimedOut()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// requestTimedOut handles a request that has timed out waiting for a response.
func (rr *RequestReceipt) requestTimedOut() {
	rr.mu.Lock()
	if rr.Status != RequestDelivered {
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
	defer rr.mu.Unlock()

	if rr.Status == RequestFailed {
		return
	}
	rr.Status = RequestReceiving
	if rr.PacketReceipt != nil {
		rr.PacketReceipt.mu.Lock()
		if rr.PacketReceipt.Status != ReceiptDelivered {
			rr.PacketReceipt.Status = ReceiptDelivered
			rr.PacketReceipt.Proved = true
		}
		rr.PacketReceipt.mu.Unlock()
	}
}
