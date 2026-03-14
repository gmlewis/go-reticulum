// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"
)

type maintenanceMockOutlet struct {
	mdu      int
	rtt      float64
	sendSeq  byte
	resendFn func(*Packet) (*Packet, error)
}

func (o *maintenanceMockOutlet) Send(raw []byte) (*Packet, error) {
	o.sendSeq++
	return &Packet{
		PacketHash: []byte{o.sendSeq},
		Receipt:    &PacketReceipt{},
	}, nil
}

func (o *maintenanceMockOutlet) Resend(p *Packet) (*Packet, error) {
	if o.resendFn != nil {
		return o.resendFn(p)
	}
	return p, nil
}

func (o *maintenanceMockOutlet) MDU() int       { return o.mdu }
func (o *maintenanceMockOutlet) RTT() float64   { return o.rtt }
func (o *maintenanceMockOutlet) IsUsable() bool { return true }

func TestChannelMediumRTTAdaptiveWindowGrowth(t *testing.T) {
	outlet := &maintenanceMockOutlet{mdu: 512, rtt: 0.5}
	channel := NewChannel(outlet)

	for i := 0; i < ChannelFastRateRounds; i++ {
		env, err := channel.Send(&StreamDataMessage{StreamID: 1, Data: []byte("x")})
		if err != nil {
			t.Fatalf("send failed: %v", err)
		}
		channel.packetDelivered(&PacketReceipt{Hash: env.Packet.PacketHash})
	}

	if got, want := channel.windowMax, ChannelWindowMaxMedium; got != want {
		t.Fatalf("windowMax=%v, want %v", got, want)
	}
	if got, want := channel.windowMin, ChannelWindowMinMedium; got != want {
		t.Fatalf("windowMin=%v, want %v", got, want)
	}
}

func TestChannelFastRTTAdaptiveWindowGrowth(t *testing.T) {
	outlet := &maintenanceMockOutlet{mdu: 512, rtt: 0.1}
	channel := NewChannel(outlet)

	for i := 0; i < ChannelFastRateRounds; i++ {
		env, err := channel.Send(&StreamDataMessage{StreamID: 1, Data: []byte("x")})
		if err != nil {
			t.Fatalf("send failed: %v", err)
		}
		channel.packetDelivered(&PacketReceipt{Hash: env.Packet.PacketHash})
	}

	if got, want := channel.windowMax, ChannelWindowMaxFast; got != want {
		t.Fatalf("windowMax=%v, want %v", got, want)
	}
	if got, want := channel.windowMin, ChannelWindowMinFast; got != want {
		t.Fatalf("windowMin=%v, want %v", got, want)
	}
}

func TestChannelTimeoutBackoffDecreasesWindowAndTimeout(t *testing.T) {
	outlet := &maintenanceMockOutlet{mdu: 512, rtt: 0.2}
	channel := NewChannel(outlet)
	channel.window = 8
	channel.windowMax = 12
	channel.windowMin = 2
	channel.windowFlexibility = 4

	env, err := channel.Send(&StreamDataMessage{StreamID: 1, Data: []byte("x")})
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}
	beforeTimeout := env.Packet.Receipt.Timeout

	channel.packetTimeout(&PacketReceipt{Hash: env.Packet.PacketHash})

	if got, want := channel.window, 7; got != want {
		t.Fatalf("window=%v, want %v", got, want)
	}
	if got, want := channel.windowMax, 11; got != want {
		t.Fatalf("windowMax=%v, want %v", got, want)
	}
	if env.Tries != 2 {
		t.Fatalf("tries=%v, want 2", env.Tries)
	}
	if env.Packet.Receipt.Timeout <= beforeTimeout {
		t.Fatalf("timeout did not increase: before=%f after=%f", beforeTimeout, env.Packet.Receipt.Timeout)
	}
	if env.Packet.Receipt.deliveryCallback == nil || env.Packet.Receipt.timeoutCallback == nil {
		t.Fatalf("expected callbacks to be reattached after resend")
	}
}

func TestChannelAutomaticTimeoutTriggersResend(t *testing.T) {
	resendCalls := 0
	outlet := &maintenanceMockOutlet{mdu: 512, rtt: 0.01}
	outlet.resendFn = func(p *Packet) (*Packet, error) {
		resendCalls++
		return p, nil
	}

	channel := NewChannel(outlet)

	env, err := channel.Send(&StreamDataMessage{StreamID: 1, Data: []byte("x")})
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if env.Packet == nil || env.Packet.Receipt == nil {
		t.Fatalf("expected packet receipt to be created")
	}

	env.Packet.Receipt.SetTimeout(0.02)
	env.Packet.Receipt.MarkSent(float64(time.Now().Add(-50*time.Millisecond).UnixNano()) / 1e9)
	channel.checkTimeouts()

	if resendCalls == 0 {
		t.Fatalf("expected at least one resend call from automatic timeout check")
	}
}
