// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"fmt"
	"time"
)

// linkStaleDiagnostic captures the timings that distinguish the three ways a
// link can run out of keepalives, because from the outside all three look
// identical: a link that stopped receiving.
//
//   - peer-silence: the remote peer (or the path underneath the link) stopped
//     delivering while this node kept sending keepalives on schedule.
//   - local-silence: this node stopped sending as well, which points at this
//     node's own link path stalling - a blocked goroutine, a slow write, or a
//     starved scheduler - rather than at the remote peer.
//   - watchdog-late: this node noticed the silence much later than the stale
//     timeout plus the grace period it schedules, so the watchdog that runs the
//     check was itself delayed by a local stall.
//
// This diagnostic is Go-specific. The Python implementation tears a stale link
// down without reporting these timings (RNS/Link.py:761-764), so nothing here
// changes wire behavior or parity; it exists because links that time out on a
// node with slow storage are otherwise indistinguishable from links that time
// out because the peer genuinely went away.
type linkStaleDiagnostic struct {
	// LinkID identifies the link that timed out.
	LinkID []byte

	// Initiator reports whether this node initiated the link. Only the
	// initiator sends keepalives, so outbound silence is only meaningful on
	// the initiator side.
	Initiator bool

	// RTT is the last measured round trip time, in seconds, matching the value
	// the keepalive interval is derived from.
	RTT float64

	// Keepalive is the interval this link uses to send keepalives.
	Keepalive time.Duration

	// StaleTime is the inbound silence after which the link is considered
	// stale (LinkStaleFactor * Keepalive).
	StaleTime time.Duration

	// StaleDelay is the grace period the stale branch schedules between
	// noticing the silence and tearing the link down
	// (RTT * LinkKeepaliveTimeoutFactor + LinkStaleGrace).
	StaleDelay time.Duration

	// SinceInbound is how long this link had received nothing when the timeout
	// was reported.
	SinceInbound time.Duration

	// SinceOutbound is how long this link had sent nothing when the timeout was
	// reported.
	SinceOutbound time.Duration

	// SinceKeepalive is how long since the last keepalive this link sent. It is
	// meaningful only when KeepaliveKnown is true.
	SinceKeepalive time.Duration

	// KeepaliveKnown reports whether this link has ever sent a keepalive, which
	// is the case for initiators and for non-initiators that have sent data.
	KeepaliveKnown bool
}

// Verdict names the most likely explanation for the timeout. It is a heuristic
// hint derived from the reported timings, not a protocol-level conclusion.
func (d linkStaleDiagnostic) Verdict() string {
	switch {
	case d.SinceInbound > d.StaleTime+d.StaleDelay+d.Keepalive:
		// The check itself ran more than one keepalive interval late, so the
		// watchdog goroutine was starved.
		return "watchdog-late"
	case d.Initiator && d.SinceOutbound >= d.StaleTime:
		// Nothing went out either, so this node's own link path was stalled.
		return "local-silence"
	default:
		return "peer-silence"
	}
}

// String renders the diagnostic as a single greppable log line.
func (d linkStaleDiagnostic) String() string {
	sinceKeepalive := "n/a"
	if d.KeepaliveKnown {
		sinceKeepalive = d.SinceKeepalive.Round(time.Millisecond).String()
	}
	return fmt.Sprintf(
		"verdict=%v rtt=%v keepalive=%v stale_timeout=%v stale_grace=%v initiator=%v since_inbound=%v since_outbound=%v since_keepalive=%v",
		d.Verdict(),
		time.Duration(d.RTT*float64(time.Second)).Round(time.Microsecond),
		d.Keepalive,
		d.StaleTime,
		d.StaleDelay,
		d.Initiator,
		d.SinceInbound.Round(time.Millisecond),
		d.SinceOutbound.Round(time.Millisecond),
		sinceKeepalive,
	)
}

// staleDiagnostic snapshots the timings reported when this link times out in
// the stale state. The caller must hold l.mu.
func (l *Link) staleDiagnostic(now time.Time) linkStaleDiagnostic {
	diag := linkStaleDiagnostic{
		LinkID:         l.linkID,
		Initiator:      l.initiator,
		RTT:            l.rtt,
		Keepalive:      l.keepalive,
		StaleTime:      l.staleTime,
		StaleDelay:     time.Duration(l.rtt*l.keepaliveTimeoutFactor*float64(time.Second)) + LinkStaleGrace,
		SinceInbound:   now.Sub(l.effectiveLastInbound()),
		SinceOutbound:  now.Sub(l.lastOutbound),
		KeepaliveKnown: !l.lastKeepalive.IsZero(),
	}
	// Clamp negative ages (possible under a wall-clock step backwards) so the
	// log line never reports a negative silence.
	diag.SinceInbound = max(diag.SinceInbound, 0)
	diag.SinceOutbound = max(diag.SinceOutbound, 0)
	if diag.KeepaliveKnown {
		// Subtracting a zero timestamp saturates at the maximum duration, so
		// the keepalive age is only measured once a keepalive was actually
		// sent; the log line renders it as n/a until then.
		diag.SinceKeepalive = max(now.Sub(l.lastKeepalive), 0)
	}
	return diag
}
