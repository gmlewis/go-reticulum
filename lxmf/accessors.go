// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// Peers returns a snapshot of every registered propagation peer, mirroring
// Python nomadnet's read of app.message_router.peers (Network.py:1755,1854).
// The returned slice is a defensive copy; callers may sort it without
// affecting the router's internal map. The slice order is not specified.
func (r *Router) Peers() []*Peer {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Peer, 0, len(r.peers))
	for _, peer := range r.peers {
		out = append(out, peer)
	}
	return out
}

// PeerByHash returns the propagation peer for the given destination hash, or
// nil if no such peer is registered. It mirrors Python's
// self.peers[destination_hash] lookup (Network.py:1815).
func (r *Router) PeerByHash(hash []byte) *Peer {
	if r == nil || len(hash) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.peers[string(hash)]
}

// Unpeer breaks the peering with the propagation node identified by
// destinationHash. It mirrors Python LXMRouter.unpeer(destination_hash,
// timestamp=None) (LXMRouter.py:1942): the timestamp defaults to the current
// time, and the peer is only removed when the timestamp is at least the peer's
// peering_timebase (an out-of-order unpeer with a stale timestamp is ignored).
func (r *Router) Unpeer(destinationHash []byte) {
	if r == nil || len(destinationHash) == 0 {
		return
	}
	r.unpeer(destinationHash, float64(r.now().UnixNano())/float64(time.Second))
}

// DestinationHash returns the propagation destination hash this peer
// represents. It mirrors Python LXMPeer.destination_hash (LXMPeer.py:146,219).
func (p *Peer) DestinationHash() []byte {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneBytes(p.destinationHash)
}

// Alive reports whether the peer is currently considered reachable. It
// mirrors Python LXMPeer.alive (LXMPeer.py:141,178).
func (p *Peer) Alive() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.alive
}

// LastHeard returns the seconds-since-epoch timestamp of the last time the
// peer was heard from. It mirrors Python LXMPeer.last_heard (LXMPeer.py:143).
func (p *Peer) LastHeard() float64 {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastHeard
}

// SyncTransferRate returns the measured sync transfer rate for the peer. It
// mirrors Python LXMPeer.sync_transfer_rate (LXMPeer.py:148,190).
func (p *Peer) SyncTransferRate() float64 {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.syncTransferRate
}

// LinkEstablishmentRate returns the measured link establishment rate for the
// peer. It mirrors Python LXMPeer.link_establishment_rate (LXMPeer.py:147,189).
func (p *Peer) LinkEstablishmentRate() float64 {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.linkEstablishmentRate
}

// PeeringCost returns the proof-of-work cost this peer requires to peer, or
// nil when no cost has been negotiated. It mirrors Python
// LXMPeer.peering_cost (LXMPeer.py:153,182), which is None until set.
func (p *Peer) PeeringCost() *int {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneOptionalInt(p.peeringCost)
}

// Identity returns the recalled identity for the peer's destination hash, or
// nil if the identity could not be recalled. It mirrors Python
// LXMPeer.identity (LXMPeer.py:220 — RNS.Identity.recall).
func (p *Peer) Identity() *rns.Identity {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.identity
}

// State returns the peer's current sync state machine state, one of the
// PeerState* constants. It mirrors Python LXMPeer.state (LXMPeer.py:214).
func (p *Peer) State() int {
	if p == nil {
		return PeerStateIdle
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// Link returns the active propagation link to this peer, or nil when no link
// is established. It mirrors Python LXMPeer.link (LXMPeer.py:213).
func (p *Peer) Link() *rns.Link {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.link
}

// LastSyncAttempt returns the seconds-since-epoch timestamp of the most recent
// sync attempt. It mirrors Python LXMPeer.last_sync_attempt, which nomadnet
// reads for the C-r sync grace check (Network.py:1825).
func (p *Peer) LastSyncAttempt() float64 {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastSyncAttempt
}

// NextSyncAttempt returns the seconds-since-epoch timestamp after which the
// next sync attempt may proceed. It mirrors Python LXMPeer.next_sync_attempt,
// which nomadnet reads for the C-r sync grace check (Network.py:1825).
func (p *Peer) NextSyncAttempt() float64 {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.nextSyncAttempt
}
