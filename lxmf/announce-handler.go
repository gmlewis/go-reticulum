// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import "github.com/gmlewis/go-reticulum/rns"

func (r *Router) registerAnnounceHandlers() {
	if r.transport == nil {
		return
	}
	r.transport.RegisterAnnounceHandler(r.deliveryAnnounceHandler())
}

func (r *Router) deliveryAnnounceHandler() *rns.AnnounceHandler {
	return &rns.AnnounceHandler{
		AspectFilter:     AppName + ".delivery",
		ReceivedAnnounce: r.handleDeliveryAnnounce,
	}
}

func (r *Router) handleDeliveryAnnounce(destinationHash []byte, _ *rns.Identity, appData []byte) {
	if stampCost, ok := StampCostFromAppData(appData); ok {
		r.updateStampCost(destinationHash, stampCost)
	}

	nowSeconds := float64(r.now().UnixNano()) / 1e9
	shouldProcess := false

	r.mu.Lock()
	for _, message := range r.pendingOutbound {
		if message == nil || message.Destination == nil {
			continue
		}
		if !equalHashes(message.Destination.Hash, destinationHash) {
			continue
		}
		if message.Method == MethodDirect || message.Method == MethodOpportunistic {
			message.NextDeliveryAttempt = nowSeconds
			shouldProcess = true
		}
	}
	r.mu.Unlock()

	if shouldProcess {
		go r.processOutbound()
	}
}

func (r *Router) updateStampCost(destinationHash []byte, stampCost int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if stampCost <= 0 {
		delete(r.outboundStampCosts, string(destinationHash))
		return
	}
	r.outboundStampCosts[string(destinationHash)] = stampCost
}

// OutboundStampCost returns the most recently announced inbound stamp cost for
// a remote LXMF delivery destination.
func (r *Router) OutboundStampCost(destinationHash []byte) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stampCost, ok := r.outboundStampCosts[string(destinationHash)]
	return stampCost, ok
}

func equalHashes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
