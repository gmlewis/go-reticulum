// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

// Link traffic counters and accessors.
//
// This file is the Go port of Python Link.py's per-link traffic accounting:
// self.tx / self.rx (packet counts) and self.txbytes / self.rxbytes (byte
// counts), initialized to zero in Link.__init__ (Link.py:250-253). Python
// increments the outbound counters in Packet.send when the destination is a
// LINK (Packet.py:294-295: self.destination.tx += 1;
// self.destination.txbytes += len(self.ciphertext)) and the inbound counters
// in Link.receive (Link.py:937-938: self.rx += 1;
// self.rxbytes += len(packet.data)). The accessors below expose the four
// counters; the increments themselves live in Link.send / Packet.Send (out)
// and Link.receive (in).

// GetTX returns the number of packets transmitted over this link. It is the
// Go port of Python's Link.tx (Link.py:250), incremented in Packet.send for a
// LINK destination (Packet.py:294).
func (l *Link) GetTX() uint64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tx
}

// GetRX returns the number of packets received over this link. It is the Go
// port of Python's Link.rx (Link.py:251), incremented in Link.receive
// (Link.py:937).
func (l *Link) GetRX() uint64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rx
}

// GetTXBytes returns the cumulative transmitted ciphertext byte count for this
// link. It is the Go port of Python's Link.txbytes (Link.py:252), incremented
// by len(self.ciphertext) in Packet.send for a LINK destination
// (Packet.py:295).
func (l *Link) GetTXBytes() uint64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.txbytes
}

// GetRXBytes returns the cumulative received data byte count for this link.
// It is the Go port of Python's Link.rxbytes (Link.py:253), incremented by
// len(packet.data) in Link.receive (Link.py:938).
func (l *Link) GetRXBytes() uint64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rxbytes
}

// recordOutbound increments the link's outbound traffic counters for one
// sent packet, mirroring Python's Packet.send LINK-destination branch
// (Packet.py:294-295): self.destination.tx += 1;
// self.destination.txbytes += len(self.ciphertext). It is called from
// Link.send (for internally-generated link packets that bypass Packet.Send
// via the attached-interface path) and from Packet.Send (for externally
// generated link packets such as resource advertisements/parts). The caller
// must NOT hold l.mu; it is taken here so the counter read in
// accumulateEstablishmentCost's neighborhood stays consistent.
func (l *Link) recordOutbound(ciphertextLen int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.tx++
	l.txbytes += uint64(ciphertextLen)
	l.mu.Unlock()
}
