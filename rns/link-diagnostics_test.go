// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"
)

func TestLinkStaleDiagnosticVerdict(t *testing.T) {
	t.Parallel()

	const keepalive = 5 * time.Second
	staleTime := 2 * keepalive
	staleDelay := LinkStaleGrace

	tests := []struct {
		name string
		diag linkStaleDiagnostic
		want string
	}{
		{
			name: "peer silence while this node kept sending keepalives",
			diag: linkStaleDiagnostic{
				Initiator:     true,
				Keepalive:     keepalive,
				StaleTime:     staleTime,
				StaleDelay:    staleDelay,
				SinceInbound:  staleTime + staleDelay,
				SinceOutbound: keepalive,
			},
			want: "peer-silence",
		},
		{
			name: "local silence when the initiator sent nothing either",
			diag: linkStaleDiagnostic{
				Initiator:     true,
				Keepalive:     keepalive,
				StaleTime:     staleTime,
				StaleDelay:    staleDelay,
				SinceInbound:  staleTime + staleDelay,
				SinceOutbound: staleTime + time.Second,
			},
			want: "local-silence",
		},
		{
			name: "watchdog late when the silence was noticed more than a keepalive past the grace",
			diag: linkStaleDiagnostic{
				Initiator:     true,
				Keepalive:     keepalive,
				StaleTime:     staleTime,
				StaleDelay:    staleDelay,
				SinceInbound:  staleTime + staleDelay + keepalive + time.Millisecond,
				SinceOutbound: keepalive,
			},
			want: "watchdog-late",
		},
		{
			name: "watchdog late wins over local silence",
			diag: linkStaleDiagnostic{
				Initiator:     true,
				Keepalive:     keepalive,
				StaleTime:     staleTime,
				StaleDelay:    staleDelay,
				SinceInbound:  staleTime + staleDelay + 2*keepalive,
				SinceOutbound: staleTime + 2*keepalive,
			},
			want: "watchdog-late",
		},
		{
			name: "a responder that never sent is not local silence",
			diag: linkStaleDiagnostic{
				Initiator:     false,
				Keepalive:     keepalive,
				StaleTime:     staleTime,
				StaleDelay:    staleDelay,
				SinceInbound:  staleTime + staleDelay,
				SinceOutbound: time.Hour,
			},
			want: "peer-silence",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.diag.Verdict(); got != tt.want {
				t.Errorf("Verdict() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLinkStaleDiagnosticString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		diag linkStaleDiagnostic
		want string
	}{
		{
			name: "reports every timing and the verdict",
			diag: linkStaleDiagnostic{
				LinkID:         []byte{0xab, 0xcd},
				Initiator:      true,
				RTT:            0.012345,
				Keepalive:      5 * time.Second,
				StaleTime:      10 * time.Second,
				StaleDelay:     5 * time.Second,
				SinceInbound:   15 * time.Second,
				SinceOutbound:  6 * time.Second,
				SinceKeepalive: 5123 * time.Millisecond,
				KeepaliveKnown: true,
			},
			want: "verdict=peer-silence rtt=12.345ms keepalive=5s stale_timeout=10s" +
				" stale_grace=5s initiator=true since_inbound=15s since_outbound=6s" +
				" since_keepalive=5.123s",
		},
		{
			name: "keepalive age is reported as n/a before any keepalive was sent",
			diag: linkStaleDiagnostic{
				LinkID:        []byte{0x01},
				Initiator:     false,
				RTT:           0.002,
				Keepalive:     5 * time.Second,
				StaleTime:     10 * time.Second,
				StaleDelay:    5 * time.Second,
				SinceInbound:  15 * time.Second,
				SinceOutbound: 15 * time.Second,
			},
			want: "verdict=peer-silence rtt=2ms keepalive=5s stale_timeout=10s" +
				" stale_grace=5s initiator=false since_inbound=15s since_outbound=15s" +
				" since_keepalive=n/a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.diag.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLinkStaleDiagnosticSnapshot(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 16, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		link          *Link
		proofTime     time.Time
		wantInbound   time.Duration
		wantOutbound  time.Duration
		wantKnown     bool
		wantKeepalive time.Duration
		wantSinceKeep time.Duration
	}{
		{
			name: "activation time bounds the inbound silence",
			link: &Link{
				linkID:        []byte{0x01},
				initiator:     true,
				rtt:           0.010,
				keepalive:     5 * time.Second,
				staleTime:     10 * time.Second,
				activatedAt:   now.Add(-12 * time.Second),
				lastOutbound:  now.Add(-4 * time.Second),
				lastKeepalive: now.Add(-4 * time.Second),
			},
			wantInbound:   12 * time.Second,
			wantOutbound:  4 * time.Second,
			wantKnown:     true,
			wantKeepalive: 5 * time.Second,
			wantSinceKeep: 4 * time.Second,
		},
		{
			name: "proof time wins over activation and inbound",
			link: &Link{
				linkID:       []byte{0x02},
				rtt:          0.100,
				keepalive:    5 * time.Second,
				staleTime:    10 * time.Second,
				activatedAt:  now.Add(-30 * time.Second),
				lastInbound:  now.Add(-20 * time.Second),
				lastOutbound: now.Add(-25 * time.Second),
			},
			proofTime:     now.Add(-15 * time.Second),
			wantInbound:   15 * time.Second,
			wantOutbound:  25 * time.Second,
			wantKnown:     false,
			wantKeepalive: 5 * time.Second,
			wantSinceKeep: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !tt.proofTime.IsZero() {
				tt.link.noteProofReceived(tt.proofTime)
			}
			got := tt.link.staleDiagnostic(now)

			if got.SinceInbound != tt.wantInbound {
				t.Errorf("SinceInbound = %v, want %v", got.SinceInbound, tt.wantInbound)
			}
			if got.SinceOutbound != tt.wantOutbound {
				t.Errorf("SinceOutbound = %v, want %v", got.SinceOutbound, tt.wantOutbound)
			}
			if got.KeepaliveKnown != tt.wantKnown {
				t.Errorf("KeepaliveKnown = %v, want %v", got.KeepaliveKnown, tt.wantKnown)
			}
			if got.Keepalive != tt.wantKeepalive {
				t.Errorf("Keepalive = %v, want %v", got.Keepalive, tt.wantKeepalive)
			}
			if got.SinceKeepalive != tt.wantSinceKeep {
				t.Errorf("SinceKeepalive = %v, want %v", got.SinceKeepalive, tt.wantSinceKeep)
			}
			wantDelay := time.Duration(tt.link.rtt*tt.link.keepaliveTimeoutFactor*float64(time.Second)) + LinkStaleGrace
			if got.StaleDelay != wantDelay {
				t.Errorf("StaleDelay = %v, want %v", got.StaleDelay, wantDelay)
			}
		})
	}
}

func TestEffectiveLastInbound(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.September, 12, 16, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		link      *Link
		proofTime time.Time
		want      time.Time
	}{
		{
			name: "activation is used when nothing arrived later",
			link: &Link{activatedAt: base},
			want: base,
		},
		{
			name: "inbound traffic wins over activation",
			link: &Link{activatedAt: base, lastInbound: base.Add(time.Second)},
			want: base.Add(time.Second),
		},
		{
			name: "inbound traffic wins over a stale activation",
			link: &Link{activatedAt: base.Add(-time.Minute), lastInbound: base},
			want: base,
		},
		{
			name: "proof wins over both",
			link: &Link{
				activatedAt: base,
				lastInbound: base.Add(time.Second),
			},
			proofTime: base.Add(2 * time.Second),
			want:      base.Add(2 * time.Second),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !tt.proofTime.IsZero() {
				tt.link.noteProofReceived(tt.proofTime)
			}
			if got := tt.link.effectiveLastInbound(); !got.Equal(tt.want) {
				t.Errorf("effectiveLastInbound() = %v, want %v", got, tt.want)
			}
		})
	}
}
