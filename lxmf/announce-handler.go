// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"fmt"
	"time"

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
		AspectFilter:         AppName + ".delivery",
		ReceivePathResponses: true,
		ReceivedAnnounce:     r.handleDeliveryAnnounce,
	}
}

func (r *Router) handleDeliveryAnnounce(destinationHash []byte, _ *rns.Identity, appData []byte) {
	if stampCost, ok, clear, err := stampCostFromAppDataOutcome(appData); err != nil {
		var logger *rns.Logger
		if r.transport != nil {
			logger = r.transport.GetLogger()
		}
		logger.Error("An error occurred while trying to decode announced stamp cost. The contained exception was: %v", err)
	} else if ok {
		r.updateStampCost(destinationHash, stampCost)
	} else if clear {
		r.clearStampCost(destinationHash)
	}

	nowSeconds := float64(r.now().UnixNano()) / 1e9
	shouldProcess := false

	r.mu.Lock()
	for _, message := range r.pendingOutbound {
		if message == nil {
			continue
		}
		messageDestinationHash := message.DestinationHash
		if len(messageDestinationHash) == 0 && message.Destination != nil {
			messageDestinationHash = message.Destination.Hash
		}
		if !equalHashes(messageDestinationHash, destinationHash) {
			continue
		}
		if message.Method == MethodDirect || message.Method == MethodOpportunistic {
			message.NextDeliveryAttempt = nowSeconds
			shouldProcess = true
		}
	}
	r.mu.Unlock()

	if shouldProcess {
		go func() {
			sleep := r.outboundTriggerSleep
			if sleep == nil {
				sleep = time.Sleep
			}
			for r.outboundProcessingActive.Load() {
				sleep(100 * time.Millisecond)
			}
			r.processOutbound()
		}()
	}
}

func (r *Router) propagationAnnounceHandler() *rns.AnnounceHandler {
	return &rns.AnnounceHandler{
		AspectFilter:                AppName + ".propagation",
		ReceivePathResponses:        true,
		ReceivedAnnounceWithContext: r.handlePropagationAnnounceWithContext,
	}
}

func (r *Router) handlePropagationAnnounce(destinationHash []byte, _ *rns.Identity, appData []byte) {
	r.handlePropagationAnnounceWithContext(destinationHash, nil, appData, false)
}

func (r *Router) handlePropagationAnnounceWithContext(destinationHash []byte, _ *rns.Identity, appData []byte, isPathResponse bool) {
	var logger *rns.Logger
	if r.transport != nil {
		logger = r.transport.GetLogger()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Debug("Error while evaluating propagation node announce, ignoring announce.")
			logger.Debug("The contained exception was: %v", fmt.Sprint(recovered))
		}
	}()

	if !r.propagationEnabled || len(appData) == 0 {
		return
	}

	announceData, ok := decodePropagationAnnounceData(appData, logger)
	if !ok {
		return
	}

	if _, staticPeer := r.staticPeers[string(destinationHash)]; staticPeer {
		peer := r.peers[string(destinationHash)]
		if !isPathResponse || peer == nil || peer.lastHeard == 0 {
			r.peer(destinationHash, announceData)
		}
		return
	}

	if !r.autopeer || isPathResponse {
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
	r.outboundStampCosts[string(destinationHash)] = outboundStampCostEntry{
		updatedAt: r.now(),
		stampCost: stampCost,
	}
}

func (r *Router) clearStampCost(destinationHash []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.outboundStampCosts, string(destinationHash))
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

func decodePropagationAnnounceData(appData []byte, logger *rns.Logger) (propagationAnnounceData, bool) {
	unpacked, err := msgpack.Unpack(appData)
	if err != nil {
		logger.Debug("Could not validate propagation node announce data: %v", err)
		return propagationAnnounceData{}, false
	}
	items, ok := unpacked.([]any)
	if !ok || len(items) < 7 {
		logger.Debug("Could not validate propagation node announce data: Invalid announce data: Insufficient peer data, likely from deprecated LXMF version")
		return propagationAnnounceData{}, false
	}

	nodeTimebase, err := anyToFloat64(items[1])
	if err != nil {
		logger.Debug("Could not validate propagation node announce data: Invalid announce data: Could not decode timebase")
		return propagationAnnounceData{}, false
	}
	propagationEnabled, ok := items[2].(bool)
	if !ok {
		logger.Debug("Could not validate propagation node announce data: Invalid announce data: Indeterminate propagation node status")
		return propagationAnnounceData{}, false
	}
	propagationTransferLimit, err := anyToFloat64(items[3])
	if err != nil {
		logger.Debug("Could not validate propagation node announce data: Invalid announce data: Could not decode propagation transfer limit")
		return propagationAnnounceData{}, false
	}

	if items[4] == nil {
		logger.Debug("Could not validate propagation node announce data: Invalid announce data: Could not decode propagation sync limit")
		return propagationAnnounceData{}, false
	}
	value, err := anyToInt(items[4])
	if err != nil {
		logger.Debug("Could not validate propagation node announce data: Invalid announce data: Could not decode propagation sync limit")
		return propagationAnnounceData{}, false
	}
	propagationSyncLimit := &value

	stampCostItems, ok := items[5].([]any)
	if !ok || len(stampCostItems) < 3 {
		logger.Debug("Could not validate propagation node announce data: Invalid announce data: Could not decode stamp costs")
		return propagationAnnounceData{}, false
	}
	propagationStampCost, err := anyToInt(stampCostItems[0])
	if err != nil {
		logger.Debug("Could not validate propagation node announce data: Invalid announce data: Could not decode target stamp cost")
		return propagationAnnounceData{}, false
	}
	propagationStampCostFlexibility, err := anyToInt(stampCostItems[1])
	if err != nil {
		logger.Debug("Could not validate propagation node announce data: Invalid announce data: Could not decode stamp cost flexibility")
		return propagationAnnounceData{}, false
	}
	peeringCost, err := anyToInt(stampCostItems[2])
	if err != nil {
		logger.Debug("Could not validate propagation node announce data: Invalid announce data: Could not decode peering cost")
		return propagationAnnounceData{}, false
	}
	metadata := peerMetadata(items[6])
	if metadata == nil {
		logger.Debug("Could not validate propagation node announce data: Invalid announce data: Could not decode metadata")
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
		metadata:                        metadata,
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
