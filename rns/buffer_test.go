// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"io"
	"testing"
)

type mockOutlet struct {
	mdu    int
	rtt    float64
	onSend func([]byte)
}

func (o *mockOutlet) Send(raw []byte) (*Packet, error) {
	if o.onSend != nil {
		o.onSend(raw)
	}
	return &Packet{PacketHash: []byte("hash"), Receipt: &PacketReceipt{}}, nil
}
func (o *mockOutlet) Resend(p *Packet) (*Packet, error) { return p, nil }
func (o *mockOutlet) MDU() int                          { return o.mdu }
func (o *mockOutlet) RTT() float64                      { return o.rtt }
func (o *mockOutlet) IsUsable() bool                    { return true }

func TestBuffer(t *testing.T) {
	outlet := &mockOutlet{mdu: 500, rtt: 0.1}
	channel := NewChannel(outlet)

	reader := Buffer.CreateReader(1, channel)
	writer := Buffer.CreateWriter(1, channel)

	// Simulate loopback
	outlet.onSend = func(raw []byte) {
		channel.Receive(raw)
	}

	msg := []byte("hello world")
	go func() {
		if _, err := writer.Write(msg); err != nil {
			t.Errorf("writer.Write() failed: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Errorf("writer.Close() failed: %v", err)
		}
	}()

	buf := make([]byte, 100)
	n, err := reader.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}

	if !bytes.Equal(msg, buf[:n]) {
		t.Errorf("expected %v, got %v", string(msg), string(buf[:n]))
	}
}

func TestBidirectionalBuffer(t *testing.T) {
	outlet1 := &mockOutlet{mdu: 500, rtt: 0.1}
	channel1 := NewChannel(outlet1)

	outlet2 := &mockOutlet{mdu: 500, rtt: 0.1}
	channel2 := NewChannel(outlet2)

	// Connect them
	outlet1.onSend = func(raw []byte) {
		channel2.Receive(raw)
	}
	outlet2.onSend = func(raw []byte) {
		channel1.Receive(raw)
	}

	bb1 := Buffer.CreateBidirectionalBuffer(1, 2, channel1)
	bb2 := Buffer.CreateBidirectionalBuffer(2, 1, channel2)

	msg := []byte("ping")
	go func() {
		if _, err := bb1.Write(msg); err != nil {
			t.Errorf("bb1.Write() failed: %v", err)
		}
	}()

	buf := make([]byte, 100)
	n, err := bb2.Read(buf)
	mustTest(t, err)

	if !bytes.Equal(msg, buf[:n]) {
		t.Errorf("expected %v, got %v", string(msg), string(buf[:n]))
	}

	reply := []byte("pong")
	go func() {
		if _, err := bb2.Write(reply); err != nil {
			t.Errorf("bb2.Write() failed: %v", err)
		}
	}()

	n, err = bb1.Read(buf)
	mustTest(t, err)

	if !bytes.Equal(reply, buf[:n]) {
		t.Errorf("expected %v, got %v", string(reply), string(buf[:n]))
	}
}

func TestBidirectionalBufferCloseRemovesOnlyItsHandler(t *testing.T) {
	outlet := &mockOutlet{mdu: 500, rtt: 0.1}
	channel := NewChannel(outlet)

	bb1 := Buffer.CreateBidirectionalBuffer(1, 2, channel)
	bb2 := Buffer.CreateBidirectionalBuffer(3, 4, channel)

	if got, want := len(channel.messageHandlers), 2; got != want {
		t.Fatalf("expected %v handlers before close, got %v", want, got)
	}

	if err := bb1.Close(); err != nil {
		t.Fatalf("bb1 close failed: %v", err)
	}

	if got, want := len(channel.messageHandlers), 1; got != want {
		t.Fatalf("expected %v handler after closing one buffer, got %v", want, got)
	}

	if err := bb2.Close(); err != nil {
		t.Fatalf("bb2 close failed: %v", err)
	}

	if got, want := len(channel.messageHandlers), 0; got != want {
		t.Fatalf("expected %v handlers after closing both buffers, got %v", want, got)
	}
}

func TestStreamDataMessageUnpackDecompressesCompressedPayload(t *testing.T) {
	original := bytes.Repeat([]byte("abc123"), 128)
	compressed, err := CompressBzip2(original, 0)
	if err != nil {
		t.Fatalf("CompressBzip2 error: %v", err)
	}

	encoded, err := (&StreamDataMessage{StreamID: 7, Data: compressed, Compressed: true}).Pack()
	if err != nil {
		t.Fatalf("pack error: %v", err)
	}

	var decoded StreamDataMessage
	if err := decoded.Unpack(encoded); err != nil {
		t.Fatalf("unpack error: %v", err)
	}

	if !decoded.Compressed {
		t.Fatalf("expected compressed flag to be preserved")
	}
	if !bytes.Equal(decoded.Data, original) {
		t.Fatalf("decompressed payload mismatch")
	}
}

func TestChannelWriterCompressesWhenSmaller(t *testing.T) {
	var sawCompressed bool
	outlet := &mockOutlet{mdu: 5000, rtt: 0.1}
	channel := NewChannel(outlet)
	reader := Buffer.CreateReader(1, channel)
	writer := Buffer.CreateWriter(1, channel)

	outlet.onSend = func(raw []byte) {
		env := &Envelope{Raw: raw}
		if err := env.Unpack(map[uint16]func() Message{SMTStreamData: func() Message { return &StreamDataMessage{} }}); err == nil {
			if sm, ok := env.Message.(*StreamDataMessage); ok && sm.Compressed {
				sawCompressed = true
			}
		}
		channel.Receive(raw)
	}

	original := bytes.Repeat([]byte("A"), 4096)
	if _, err := writer.Write(original); err != nil {
		t.Fatalf("write error: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close error: %v", err)
	}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("stream payload mismatch")
	}
	if !sawCompressed {
		t.Fatalf("expected at least one compressed stream chunk")
	}
}

func TestChannelWriterCompressionCanBeDisabled(t *testing.T) {
	outlet := &mockOutlet{mdu: 5000, rtt: 0.1}
	channel := NewChannel(outlet)
	reader := Buffer.CreateReader(2, channel)
	writer := Buffer.CreateWriterWithOptions(2, channel, ChannelWriterOptions{EnableCompression: false})

	outlet.onSend = func(raw []byte) {
		env := &Envelope{Raw: raw}
		if err := env.Unpack(map[uint16]func() Message{SMTStreamData: func() Message { return &StreamDataMessage{} }}); err == nil {
			if sm, ok := env.Message.(*StreamDataMessage); ok && sm.Compressed {
				t.Fatalf("expected uncompressed stream chunk when compression disabled")
			}
		}
		channel.Receive(raw)
	}

	original := bytes.Repeat([]byte("A"), 4096)
	if _, err := writer.Write(original); err != nil {
		t.Fatalf("write error: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close error: %v", err)
	}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("stream payload mismatch")
	}
}
