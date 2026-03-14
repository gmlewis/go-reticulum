// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	vendoredbzip2 "github.com/gmlewis/go-reticulum/compress/bzip2"
)

// StreamDataMessage encapsulates binary stream data for reliable transmission over a Reticulum Channel.
type StreamDataMessage struct {
	StreamID   uint16
	Data       []byte
	EOF        bool
	Compressed bool
}

// GetMsgType retrieves the internal message type identifier for the stream data message.
func (m *StreamDataMessage) GetMsgType() uint16 {
	return SMTStreamData
}

// StreamIDMax defines the maximum allowable stream identifier value within the protocol limits.
const StreamIDMax = 0x3fff

const (
	bufferMaxChunkLen      = 16 * 1024
	bufferCompressionTries = 4
)

// DefaultChannelWriterCompressionEnabled controls whether ChannelWriter instances created without explicit options attempt bzip2 compression by default.
var DefaultChannelWriterCompressionEnabled = true

// ChannelWriterOptions configures the runtime stream chunk compression behavior for a ChannelWriter.
type ChannelWriterOptions struct {
	EnableCompression bool
}

// Pack serializes the StreamDataMessage into a compact binary envelope for network transmission.
func (m *StreamDataMessage) Pack() ([]byte, error) {
	if m.StreamID > StreamIDMax {
		return nil, errors.New("stream ID too large")
	}

	headerVal := m.StreamID & 0x3fff
	if m.EOF {
		headerVal |= 0x8000
	}
	if m.Compressed {
		headerVal |= 0x4000
	}

	out := make([]byte, 2+len(m.Data))
	binary.BigEndian.PutUint16(out[0:2], headerVal)
	copy(out[2:], m.Data)
	return out, nil
}

// Unpack reconstructs the StreamDataMessage state by parsing the provided binary envelope and automatically handling decompression if required.
func (m *StreamDataMessage) Unpack(data []byte) error {
	if len(data) < 2 {
		return errors.New("stream data message too short")
	}

	headerVal := binary.BigEndian.Uint16(data[0:2])
	m.EOF = (headerVal & 0x8000) != 0
	m.Compressed = (headerVal & 0x4000) != 0
	m.StreamID = headerVal & 0x3fff
	m.Data = data[2:]
	if m.Compressed {
		decompressed, err := DecompressBzip2(m.Data)
		if err != nil {
			return fmt.Errorf("failed to decompress stream data: %w", err)
		}
		m.Data = decompressed
	}
	return nil
}

// ChannelReader provides an io.Reader implementation that safely consumes incoming binary stream data from a Reticulum Channel.
type ChannelReader struct {
	streamID  uint16
	channel   *Channel
	handlerID uint64
	buffer    []byte
	eof       bool
	mu        sync.Mutex
	cond      *sync.Cond
}

// NewChannelReader initializes and registers a new ChannelReader to listen for incoming stream data on the specified channel.
func NewChannelReader(streamID uint16, channel *Channel) *ChannelReader {
	cr := &ChannelReader{
		streamID: streamID,
		channel:  channel,
	}
	cr.cond = sync.NewCond(&cr.mu)

	channel.RegisterMessageType(SMTStreamData, func() Message { return &StreamDataMessage{} })
	cr.handlerID = channel.addMessageHandler(cr.handleMessage)

	return cr
}

func (cr *ChannelReader) handleMessage(msg Message) bool {
	sm, ok := msg.(*StreamDataMessage)
	if !ok || sm.StreamID != cr.streamID {
		return false
	}

	cr.mu.Lock()
	defer cr.mu.Unlock()

	if len(sm.Data) > 0 {
		cr.buffer = append(cr.buffer, sm.Data...)
	}
	if sm.EOF {
		cr.eof = true
	}
	cr.cond.Broadcast()
	return true
}

// Read copies available stream data into the provided byte slice, blocking if necessary until data arrives or the stream ends.
func (cr *ChannelReader) Read(p []byte) (n int, err error) {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	for len(cr.buffer) == 0 && !cr.eof {
		cr.cond.Wait()
	}

	if len(cr.buffer) == 0 && cr.eof {
		return 0, io.EOF
	}

	n = copy(p, cr.buffer)
	cr.buffer = cr.buffer[n:]
	return n, nil
}

// ChannelWriter provides an io.Writer implementation that chunks, optionally compresses, and securely transmits binary stream data over a Reticulum Channel.
type ChannelWriter struct {
	streamID          uint16
	channel           *Channel
	enableCompression bool
	mu                sync.Mutex
}

// NewChannelWriter instantiates a new ChannelWriter using the default stream chunk compression settings.
func NewChannelWriter(streamID uint16, channel *Channel) *ChannelWriter {
	return NewChannelWriterWithOptions(streamID, channel, ChannelWriterOptions{EnableCompression: DefaultChannelWriterCompressionEnabled})
}

// NewChannelWriterWithOptions instantiates a new ChannelWriter with explicit configuration options for stream chunk compression.
func NewChannelWriterWithOptions(streamID uint16, channel *Channel, opts ChannelWriterOptions) *ChannelWriter {
	return &ChannelWriter{
		streamID:          streamID,
		channel:           channel,
		enableCompression: opts.EnableCompression,
	}
}

// Write safely chunks and transmits the provided byte slice across the underlying channel, applying compression if enabled and beneficial.
func (cw *ChannelWriter) Write(p []byte) (n int, err error) {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	mdu := cw.channel.outlet.MDU() - 6 - 2 // Envelope overhead + StreamDataMessage overhead
	if mdu <= 0 {
		return 0, errors.New("channel MDU too small for stream data")
	}

	total := len(p)
	sent := 0
	for sent < total {
		remaining := p[sent:]
		candidateLen := len(remaining)
		if candidateLen > bufferMaxChunkLen {
			candidateLen = bufferMaxChunkLen
		}

		chunk := remaining[:candidateLen]
		processedLen := candidateLen
		compressed := false

		if cw.enableCompression {
			for compTry := 1; candidateLen > 32 && compTry < bufferCompressionTries; compTry++ {
				segmentLen := candidateLen / compTry
				if segmentLen <= 0 {
					continue
				}

				compressedChunk, compressErr := CompressBzip2(remaining[:segmentLen], vendoredbzip2.DefaultCompression)
				if compressErr != nil {
					return sent, compressErr
				}

				if len(compressedChunk) < mdu && len(compressedChunk) < segmentLen {
					chunk = compressedChunk
					processedLen = segmentLen
					compressed = true
					break
				}
			}
		}

		if !compressed {
			if processedLen > mdu {
				processedLen = mdu
			}
			chunk = remaining[:processedLen]
		}

		msg := &StreamDataMessage{
			StreamID:   cw.streamID,
			Data:       chunk,
			Compressed: compressed,
		}

		if _, err := cw.channel.Send(msg); err != nil {
			return sent, err
		}
		sent += processedLen
	}

	return sent, nil
}

// Close signals the end of the stream transmission to the remote peer, ensuring any pending operations are finalized.
func (cw *ChannelWriter) Close() error {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	msg := &StreamDataMessage{
		StreamID: cw.streamID,
		EOF:      true,
	}
	_, err := cw.channel.Send(msg)
	return err
}

// BidirectionalBuffer provides a synchronized, full-duplex io.ReadWriteCloser stream implementation built on top of a Reticulum Channel.
type BidirectionalBuffer struct {
	*ChannelReader
	*ChannelWriter
}

// Close cleanly shuts down both the reading and writing halves of the bidirectional buffer, releasing underlying resources.
func (bb *BidirectionalBuffer) Close() error {
	bb.ChannelReader.channel.removeMessageHandlerByID(bb.ChannelReader.handlerID)
	return bb.ChannelWriter.Close()
}

// Buffer provides an organized namespace of factory functions for effortlessly creating reader, writer, and bidirectional stream abstractions over a Channel.
var Buffer = struct {
	CreateReader              func(streamID uint16, channel *Channel) *ChannelReader
	CreateWriter              func(streamID uint16, channel *Channel) *ChannelWriter
	CreateWriterWithOptions   func(streamID uint16, channel *Channel, opts ChannelWriterOptions) *ChannelWriter
	CreateBidirectionalBuffer func(rxStreamID, txStreamID uint16, channel *Channel) *BidirectionalBuffer
}{
	CreateReader: func(streamID uint16, channel *Channel) *ChannelReader {
		return NewChannelReader(streamID, channel)
	},
	CreateWriter: func(streamID uint16, channel *Channel) *ChannelWriter {
		return NewChannelWriter(streamID, channel)
	},
	CreateWriterWithOptions: func(streamID uint16, channel *Channel, opts ChannelWriterOptions) *ChannelWriter {
		return NewChannelWriterWithOptions(streamID, channel, opts)
	},
	CreateBidirectionalBuffer: func(rxStreamID, txStreamID uint16, channel *Channel) *BidirectionalBuffer {
		return &BidirectionalBuffer{
			ChannelReader: NewChannelReader(rxStreamID, channel),
			ChannelWriter: NewChannelWriter(txStreamID, channel),
		}
	},
}
