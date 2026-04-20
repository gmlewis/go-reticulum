// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

func (r *Router) registerAnnounceHandlers() {
	if r.transport == nil {
		return
	}
	r.transport.RegisterAnnounceHandler(r.deliveryAnnounceHandler())
	r.transport.RegisterAnnounceHandler(r.propagationAnnounceHandler())
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

func (r *Router) propagationAnnounceHandler() *rns.AnnounceHandler {
	return &rns.AnnounceHandler{
		AspectFilter:     AppName + ".propagation",
		ReceivedAnnounce: r.handlePropagationAnnounce,
	}
}

func (r *Router) handlePropagationAnnounce(destinationHash []byte, _ *rns.Identity, appData []byte) {
	if !r.propagationEnabled || len(appData) == 0 {
		return
	}

	announceData, ok := decodePropagationAnnounceData(appData)
	if !ok {
		return
	}

	if _, staticPeer := r.staticPeers[string(destinationHash)]; staticPeer {
		peer := r.peers[string(destinationHash)]
		if peer == nil || peer.lastHeard == 0 {
			r.peer(destinationHash, announceData)
		}
		return
	}

	if !r.autopeer {
		return
	}

	if announceData.propagationEnabled {
		hops := rns.PathfinderM
		if r.hopsTo != nil {
			hops = r.hopsTo(destinationHash)
		}
		if hops <= r.autopeerMaxdepth {
			r.peer(destinationHash, announceData)
			return
		}
		if _, exists := r.peers[string(destinationHash)]; exists {
			r.unpeer(destinationHash, announceData.nodeTimebase)
		}
		return
	}

	r.unpeer(destinationHash, announceData.nodeTimebase)
}

func (r *Router) updateStampCost(destinationHash []byte, stampCost int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if stampCost <= 0 {
		delete(r.outboundStampCosts, string(destinationHash))
		return
	}
	r.outboundStampCosts[string(destinationHash)] = outboundStampCostEntry{
		updatedAt: r.now(),
		stampCost: stampCost,
	}
}

// OutboundStampCost returns the most recently announced inbound stamp cost for
// a remote LXMF delivery destination.
func (r *Router) OutboundStampCost(destinationHash []byte) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.outboundStampCosts[string(destinationHash)]
	return entry.stampCost, ok
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

type propagationAnnounceData struct {
	nodeTimebase                    float64
	propagationEnabled              bool
	propagationTransferLimit        float64
	propagationSyncLimit            *int
	propagationStampCost            int
	propagationStampCostFlexibility int
	peeringCost                     int
	metadata                        map[any]any
}

func decodePropagationAnnounceData(appData []byte) (propagationAnnounceData, bool) {
	unpacked, err := msgpack.Unpack(appData)
	if err != nil {
		return propagationAnnounceData{}, false
	}
	items, ok := unpacked.([]any)
	if !ok || len(items) < 7 {
		return propagationAnnounceData{}, false
	}

	nodeTimebase, err := anyToFloat64(items[1])
	if err != nil {
		return propagationAnnounceData{}, false
	}
	propagationEnabled, ok := items[2].(bool)
	if !ok {
		return propagationAnnounceData{}, false
	}
	propagationTransferLimit, err := anyToFloat64(items[3])
	if err != nil {
		return propagationAnnounceData{}, false
	}

	var propagationSyncLimit *int
	if items[4] != nil {
		value, err := anyToInt(items[4])
		if err != nil {
			return propagationAnnounceData{}, false
		}
		propagationSyncLimit = &value
	}

	stampCostItems, ok := items[5].([]any)
	if !ok || len(stampCostItems) < 3 {
		return propagationAnnounceData{}, false
	}
	propagationStampCost, err := anyToInt(stampCostItems[0])
	if err != nil {
		return propagationAnnounceData{}, false
	}
	propagationStampCostFlexibility, err := anyToInt(stampCostItems[1])
	if err != nil {
		return propagationAnnounceData{}, false
	}
	peeringCost, err := anyToInt(stampCostItems[2])
	if err != nil {
		return propagationAnnounceData{}, false
	}

	return propagationAnnounceData{
		nodeTimebase:                    nodeTimebase,
		propagationEnabled:              propagationEnabled,
		propagationTransferLimit:        propagationTransferLimit,
		propagationSyncLimit:            propagationSyncLimit,
		propagationStampCost:            propagationStampCost,
		propagationStampCostFlexibility: propagationStampCostFlexibility,
		peeringCost:                     peeringCost,
		metadata:                        peerMetadata(items[6]),
	}, true
}

func (r *Router) peer(destinationHash []byte, announceData propagationAnnounceData) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if announceData.peeringCost > r.maxPeeringCost {
		if _, exists := r.peers[string(destinationHash)]; exists {
			r.unpeerLocked(destinationHash, announceData.nodeTimebase)
		}
		return
	}

	if peer, exists := r.peers[string(destinationHash)]; exists && peer != nil {
		if announceData.nodeTimebase <= peer.peeringTimebase {
			return
		}
		peer.alive = true
		peer.metadata = cloneMetadata(announceData.metadata)
		peer.syncBackoff = 0
		peer.nextSyncAttempt = 0
		peer.peeringTimebase = announceData.nodeTimebase
		peer.lastHeard = peerTime(r.now())
		peer.propagationStampCost = cloneOptionalInt(&announceData.propagationStampCost)
		peer.propagationStampCostFlexibility = cloneOptionalInt(&announceData.propagationStampCostFlexibility)
		peer.peeringCost = cloneOptionalInt(&announceData.peeringCost)
		peer.propagationTransferLimit = cloneOptionalFloat64(&announceData.propagationTransferLimit)
		if announceData.propagationSyncLimit != nil {
			peer.propagationSyncLimit = cloneOptionalInt(announceData.propagationSyncLimit)
		} else {
			fallback := int(announceData.propagationTransferLimit)
			peer.propagationSyncLimit = &fallback
		}
		return
	}

	if len(r.peers) >= r.maxPeers {
		return
	}

	peer := NewPeer(r, destinationHash)
	peer.alive = true
	peer.metadata = cloneMetadata(announceData.metadata)
	peer.lastHeard = peerTime(r.now())
	peer.peeringTimebase = announceData.nodeTimebase
	peer.propagationStampCost = cloneOptionalInt(&announceData.propagationStampCost)
	peer.propagationStampCostFlexibility = cloneOptionalInt(&announceData.propagationStampCostFlexibility)
	peer.peeringCost = cloneOptionalInt(&announceData.peeringCost)
	peer.propagationTransferLimit = cloneOptionalFloat64(&announceData.propagationTransferLimit)
	if announceData.propagationSyncLimit != nil {
		peer.propagationSyncLimit = cloneOptionalInt(announceData.propagationSyncLimit)
	} else {
		fallback := int(announceData.propagationTransferLimit)
		peer.propagationSyncLimit = &fallback
	}
	r.peers[string(destinationHash)] = peer
}

func (r *Router) unpeer(destinationHash []byte, timestamp float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unpeerLocked(destinationHash, timestamp)
}

func (r *Router) unpeerLocked(destinationHash []byte, timestamp float64) {
	peer, exists := r.peers[string(destinationHash)]
	if !exists || peer == nil {
		return
	}
	if timestamp >= peer.peeringTimebase {
		delete(r.peers, string(destinationHash))
	}
}
