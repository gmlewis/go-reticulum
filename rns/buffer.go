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

// StreamDataMessage encapsulates binary stream data to be sent over a Channel.
type StreamDataMessage struct {
	StreamID   uint16
	Data       []byte
	EOF        bool
	Compressed bool
}

func (m *StreamDataMessage) GetMsgType() uint16 {
	return SMTStreamData
}

const StreamIDMax = 0x3fff

const (
	bufferMaxChunkLen      = 16 * 1024
	bufferCompressionTries = 4
)

// DefaultChannelWriterCompressionEnabled controls whether ChannelWriter instances
// created without explicit options attempt bzip2 compression.
var DefaultChannelWriterCompressionEnabled = true

// ChannelWriterOptions configures stream chunk compression behavior.
type ChannelWriterOptions struct {
	EnableCompression bool
}

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

// ChannelReader implements io.Reader for a Channel stream.
type ChannelReader struct {
	streamID  uint16
	channel   *Channel
	handlerID uint64
	buffer    []byte
	eof       bool
	mu        sync.Mutex
	cond      *sync.Cond
}

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

// ChannelWriter implements io.Writer for a Channel stream.
type ChannelWriter struct {
	streamID          uint16
	channel           *Channel
	enableCompression bool
	mu                sync.Mutex
}

func NewChannelWriter(streamID uint16, channel *Channel) *ChannelWriter {
	return NewChannelWriterWithOptions(streamID, channel, ChannelWriterOptions{EnableCompression: DefaultChannelWriterCompressionEnabled})
}

func NewChannelWriterWithOptions(streamID uint16, channel *Channel, opts ChannelWriterOptions) *ChannelWriter {
	return &ChannelWriter{
		streamID:          streamID,
		channel:           channel,
		enableCompression: opts.EnableCompression,
	}
}

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

// BidirectionalBuffer provides a full-duplex stream over a Channel.
type BidirectionalBuffer struct {
	*ChannelReader
	*ChannelWriter
}

func (bb *BidirectionalBuffer) Close() error {
	bb.ChannelReader.channel.removeMessageHandlerByID(bb.ChannelReader.handlerID)
	return bb.ChannelWriter.Close()
}

// Buffer provides helper functions for creating buffered streams over a Channel.
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
