// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"errors"
	"time"
)

// LinkPingPath is the well-known request path used by Link.Ping for its
// round-trip probe. It is an application-layer request sent over a normal
// established link, so the on-wire format is identical to any other RNS
// request/response and is fully Python-compatible. The remote endpoint must
// register a request handler for this path (returning any non-nil response)
// for a ping to succeed; Python RNS does not expose a public ping method, so
// this is a Go-port extension that go-nomadnet uses for its "ping peer" UI.
const LinkPingPath = "/__rns_link_ping__"

// GetMTU returns the Maximum Transmission Unit of an established link, or nil
// when the link is not active. It is the Go port of Python's Link.get_mtu()
// (Link.py:609), which returns None unless status == Link.ACTIVE.
func (l *Link) GetMTU() *int {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.status.Load() != LinkActive {
		return nil
	}
	mtu := l.mtu
	return &mtu
}

// GetMDU returns the Maximum Data Unit (payload size) of an established link,
// or nil when the link is not active. It is the Go port of Python's
// Link.get_mdu() (Link.py:618), which returns None unless status == ACTIVE.
func (l *Link) GetMDU() *int {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.status.Load() != LinkActive {
		return nil
	}
	mdu := l.mdu
	return &mdu
}

// GetMode returns the cryptographic mode of the link (one of LinkModeAES128CBC
// / LinkModeAES256CBC). It is the Go port of Python's Link.get_mode()
// (Link.py:636), which always returns the mode regardless of status.
func (l *Link) GetMode() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.mode
}

// GetAge returns the time in seconds since the link was activated, or nil when
// the link has not yet been activated. It is the Go port of Python's
// Link.get_age() (Link.py:648), which returns None when activated_at is unset.
func (l *Link) GetAge() *float64 {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	activated := l.activatedAt
	l.mu.Unlock()
	if activated.IsZero() {
		return nil
	}
	age := l.nowTime().Sub(activated).Seconds()
	return &age
}

// GetEstablishmentRate returns the link establishment data rate in bits per
// second, or nil when it has not yet been measured. It is the Go port of
// Python's Link.get_establishment_rate() (Link.py:600), which returns
// establishment_rate*8 (or None). establishment_rate = establishment_cost/rtt
// is computed once the RTT is known during the handshake (Link.py:436,545).
func (l *Link) GetEstablishmentRate() *float64 {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.establishmentRate <= 0 {
		return nil
	}
	rate := l.establishmentRate * 8
	return &rate
}

// GetExpectedRate returns the most recently measured in-flight data rate of
// an established link in bits per second, or nil when no resource transfer has
// completed. It is the Go port of Python's Link.get_expected_rate()
// (Link.py:594-599). Python stores expected_rate = (resource.size*8)/transfer_time
// (Link.py:1258/1261) — already in bits per second — and get_expected_rate
// returns it RAW (no extra *8), unlike get_establishment_rate whose field is
// in bytes/sec and therefore multiplied by 8.
func (l *Link) GetExpectedRate() *float64 {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.expectedRate <= 0 {
		return nil
	}
	rate := l.expectedRate
	return &rate
}

// GetSalt returns the link's salt, which is the link identifier. It is the Go
// port of Python's Link.get_salt() (Link.py:642), which returns self.link_id.
func (l *Link) GetSalt() []byte {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.linkID...)
}

// GetContext returns the link context. It is the Go port of Python's
// Link.get_context() (Link.py:645), which currently always returns None.
func (l *Link) GetContext() any {
	return nil
}

// SetNowForTest installs an alternate clock used by nowTime, overriding
// time.Now. It exists so cross-package golden tests (e.g. the lxmf
// Router.CleanLinks parity test) can advance a link's perceived time without
// sleeping or driving a real handshake, mirroring the in-package `now` field
// injection that rns tests use directly. It has no effect on wire behavior.
func (l *Link) SetNowForTest(now func() time.Time) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.now = now
	l.mu.Unlock()
}

// Ping sends an RTT probe over the established link and returns the measured
// round-trip time in seconds. It is a Go-port extension (Python RNS has no
// public ping method) that unblocks go-nomadnet's "ping peer" UI: it issues a
// normal RNS request to LinkPingPath and measures the elapsed wall-clock time
// until the response arrives. The remote endpoint must register a request
// handler for LinkPingPath for the probe to succeed; otherwise Ping returns a
// timeout error.
func (l *Link) Ping() (float64, error) {
	if l == nil {
		return 0, errors.New("ping: link is nil")
	}
	if l.GetStatus() != LinkActive {
		return 0, errors.New("ping: link is not active")
	}

	// Derive a ping-appropriate timeout from the link RTT: a few RTTs plus a
	// small fixed grace. A ping is a lightweight probe and should not wait
	// as long as a full request/response exchange.
	timeout := time.Duration(l.rtt*l.trafficTimeoutFactor*float64(time.Second)) + 3*time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	start := time.Now()
	done := make(chan int, 2) // receives the terminal receipt status

	_, err := l.Request(
		LinkPingPath,
		nil,
		func(rr *RequestReceipt) { done <- rr.GetStatus() },
		func(rr *RequestReceipt) { done <- rr.GetStatus() },
		nil,
		timeout,
		0,
	)
	if err != nil {
		return 0, err
	}

	// Backstop: give the request's own timeout job a little extra room to
	// fire its failed callback before we give up.
	select {
	case status := <-done:
		if status != RequestReady {
			return 0, errors.New("ping: no response (link not active or no ping handler on remote)")
		}
		return time.Since(start).Seconds(), nil
	case <-time.After(timeout + time.Second):
		return 0, errors.New("ping: timed out waiting for response")
	}
}

// RTT returns the measured round-trip time in seconds for an ACTIVE link,
// or 0 when the link is not yet active or RTT has not been measured. Callers
// use this to compute request/response timeouts that scale with the actual
// link latency, mirroring Python Link.py's self.rtt field. The value is set
// during link establishment (ValidateProof/HandleRTT) and may be refined by
// later RTT measurement packets.
func (l *Link) RTT() float64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rtt
}

// TrafficTimeoutFactor returns the multiplier applied to RTT when computing
// request/response timeouts (Python Link.TRAFFIC_TIMEOUT_FACTOR = 6). Callers
// use this together with RTT to compute an RTT-adaptive deadline:
// timeout = rtt * TrafficTimeoutFactor + grace.
func (l *Link) TrafficTimeoutFactor() float64 {
	if l == nil {
		return 6.0
	}
	return l.trafficTimeoutFactor
}
