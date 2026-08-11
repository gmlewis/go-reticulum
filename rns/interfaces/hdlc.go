// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"log"
)

// hdlcTruncatedHashLength mirrors RNS.Reticulum.TRUNCATED_HASHLENGTH (128
// bits). It is duplicated here because the interfaces package cannot import
// the rns package (circular dependency); the value is a wire-stable constant.
const hdlcTruncatedHashLength = 128

// HDLCHeaderMinSize is the minimum number of bytes a valid unescaped HDLC
// frame must contain (RNS.Reticulum.HEADER_MINSIZE =
// 2+1+TRUNCATED_HASHLENGTH/8 = 19). Frames at or below this length cannot
// carry a valid packet header and are dropped before inbound dispatch. It
// mirrors rns.HeaderMinSize, duplicated here to avoid the rns↔interfaces
// import cycle.
const HDLCHeaderMinSize = 2 + 1 + (hdlcTruncatedHashLength / 8)

// CheckFrameLen validates an unescaped HDLC frame's length against the
// interface's hardware MTU and IFAC size (RNS/Interfaces/{TCP,Backbone}Interface.py
// check_frame_len, v1.4.0). It returns false — meaning the frame must be
// dropped before inbound dispatch — for frames at or below HDLCHeaderMinSize
// (cannot carry a valid header) or above hwmtu+ifacSize. ifacSize is 0 when no
// IFAC is configured, matching Python's `(self.ifac_size or 0)`.
func CheckFrameLen(frameLen, hwmtu, ifacSize int) bool {
	if frameLen <= HDLCHeaderMinSize {
		return false
	}
	if frameLen > hwmtu+ifacSize {
		return false
	}
	return true
}

// InvalidFrame logs a dropped invalid HDLC frame at debug level, matching
// RNS/Interfaces/{TCP,Backbone}Interface.py invalid_frame (v1.4.0). The max
// (hwmtu+ifacSize) is included so the log line reports the configured ceiling.
func InvalidFrame(ifaceName string, frameLen, hwmtu, ifacSize int) {
	log.Printf("[HDLC] %v: invalid frame of %v bytes received (max %v), dropping frame", ifaceName, frameLen, hwmtu+ifacSize)
}

// HDLCFlag defines the High-Level Data Link Control (HDLC) frame boundary
// marker. It is the synchronization primitive used to assert the start and
// end of discrete packets over raw serial interfaces.
const HDLCFlag = 0x7E

// HDLCEsc escapes reserved bytes inside an HDLC payload.
// It prevents embedded flag markers from being misinterpreted
// as structural boundaries.
const HDLCEsc = 0x7D

// HDLCEscMask is XORed with escaped bytes in an HDLC frame.
// It ensures escaped bytes are transformed safely for transport.
const HDLCEscMask = 0x20

// HDLCEscape scans and reformats a binary payload to comply with HDLC framing
// constraints. It replaces flag and escape characters so the payload is safe for
// hardware transmission.
func HDLCEscape(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for _, b := range data {
		switch b {
		case HDLCEsc:
			out = append(out, HDLCEsc, HDLCEsc^HDLCEscMask)
		case HDLCFlag:
			out = append(out, HDLCEsc, HDLCFlag^HDLCEscMask)
		default:
			out = append(out, b)
		}
	}
	return out
}

// HDLCUnescape parses a raw byte stream and removes escape markers to restore
// the original payload. It reverses the obfuscation applied for transmission.
func HDLCUnescape(data []byte) []byte {
	out := make([]byte, 0, len(data))
	escape := false
	for _, b := range data {
		if b == HDLCEsc && !escape {
			escape = true
			continue
		}
		if escape {
			out = append(out, b^HDLCEscMask)
			escape = false
		} else {
			out = append(out, b)
		}
	}
	return out
}

// hdlcReassemble scans frameBuffer for complete HDLC frames delimited by
// HDLCFlag markers and returns the unconsumed tail (to carry across reads)
// plus the unescaped payloads of every complete frame found. It mirrors the
// inner while-flags_remaining loop of Python's read_loop
// (RNS/Interfaces/{TCP,Backbone}Interface.py, v1.4.0), including the
// frame_buffer overflow guard: when a start flag has no matching end flag and
// the buffer exceeds 2*hwmtu, the entire buffer is dropped (returning an empty
// tail) instead of being held — bounding memory against a peer that streams a
// start flag followed by unbounded flag-less junk. A buffer with no flag at
// all is likewise dropped. Frame-length validation (CheckFrameLen) is applied
// by the caller per reassembled frame.
func hdlcReassemble(frameBuffer []byte, hwmtu int) (tail []byte, frames [][]byte) {
	for {
		start := bytes.IndexByte(frameBuffer, HDLCFlag)
		if start == -1 {
			// No flag boundary at all: drop the accumulated garbage.
			return frameBuffer[:0], frames
		}
		end := bytes.IndexByte(frameBuffer[start+1:], HDLCFlag)
		if end == -1 {
			// Start flag with no end flag: an incomplete frame. Hold it for
			// more data unless it has already exceeded 2*HW_MTU, in which case
			// it cannot be a legitimate frame — drop the whole buffer.
			if len(frameBuffer) > hwmtu*2 {
				return frameBuffer[:0], frames
			}
			return frameBuffer[start:], frames
		}
		end += start + 1
		frame := frameBuffer[start+1 : end]
		unescaped := HDLCUnescape(frame)
		if len(unescaped) > 0 {
			frames = append(frames, unescaped)
		}
		frameBuffer = frameBuffer[end:]
	}
}
