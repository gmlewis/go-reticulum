// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// Constants defining the structural boundaries and escape markers required by the
// KISS (Keep It Simple, Stupid) framing protocol.
const (
	// KISSFend designates the Frame End marker, definitively signaling the start or conclusion of a discrete data packet.
	KISSFend = 0xC0
	// KISSFesc specifies the Frame Escape character, utilized to safely obfuscate control bytes appearing within the payload.
	KISSFesc = 0xDB
	// KISSTfend is the Transposed Frame End byte, structurally substituted into the stream when a literal Fend appears in the data.
	KISSTfend = 0xDC
	// KISSTfesc is the Transposed Frame Escape byte, substituted when a literal Fesc is encountered in the payload.
	KISSTfesc = 0xDD
	// KISSCmdData instructs the TNC that the accompanying payload consists of standard, routable network data rather than control commands.
	KISSCmdData = 0x00

	// KISS command bytes for radio configuration.
	KISSCmdFrequency  = 0x01
	KISSCmdBandwidth  = 0x02
	KISSCmdTXPower    = 0x03
	KISSCmdSF         = 0x04
	KISSCmdCR         = 0x05
	KISSCmdRadioState = 0x06
	KISSCmdRadioLock  = 0x07
	KISSCmdDetect     = 0x08
	KISSCmdLeave      = 0x0A
	KISSCmdSTALock    = 0x0B
	KISSCmdLTALock    = 0x0C
	KISSCmdReady      = 0x0F
	KISSCmdSelInt     = 0x1F

	// Radio state constants.
	RadioStateOff = 0x00
	RadioStateOn  = 0x01
	RadioStateAsk = 0xFF
)

// KISSEscape scans and re-encodes a binary payload to adhere to the KISS
// protocol's byte-stuffing rules. It neutralizes internal boundary markers and
// returns a buffer safe for transmission over raw serial links.
func KISSEscape(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for _, b := range data {
		switch b {
		case KISSFesc:
			out = append(out, KISSFesc, KISSTfesc)
		case KISSFend:
			out = append(out, KISSFesc, KISSTfend)
		default:
			out = append(out, b)
		}
	}
	return out
}

// KISSUnescape parses a received byte stream and strips KISS framing escapes to
// restore the original payload. It reverses the obfuscation applied during
// transmission so upper network layers receive unmodified data.
func KISSUnescape(data []byte) []byte {
	out := make([]byte, 0, len(data))
	escape := false
	for _, b := range data {
		if escape {
			switch b {
			case KISSTfend:
				out = append(out, KISSFend)
			case KISSTfesc:
				out = append(out, KISSFesc)
			}
			escape = false
		} else if b == KISSFesc {
			escape = true
		} else {
			out = append(out, b)
		}
	}
	return out
}

// KISSFrame builds a completed KISS command frame: [FEND][cmd][data...][FEND].
// The data bytes are KISS-escaped before framing.
func KISSFrame(cmd byte, data []byte) []byte {
	escaped := KISSEscape(data)
	frame := make([]byte, 0, 2+len(escaped)+1)
	frame = append(frame, KISSFend)
	frame = append(frame, cmd)
	frame = append(frame, escaped...)
	frame = append(frame, KISSFend)
	return frame
}

// KISSFrameUint32 builds a KISS command frame encoding a uint32 value in
// big-endian byte order, KISS-escaped within the frame.
func KISSFrameUint32(cmd byte, value uint32) []byte {
	data := []byte{
		byte(value >> 24),
		byte(value >> 16),
		byte(value >> 8),
		byte(value),
	}
	return KISSFrame(cmd, data)
}

// KISSFrameUint8 builds a KISS command frame encoding a single byte value.
// No KISS escaping is needed for single-byte payloads that aren't FEND/FESC.
func KISSFrameUint8(cmd byte, value byte) []byte {
	return KISSFrame(cmd, []byte{value})
}

// KISSFrameUint16 builds a KISS command frame encoding a uint16 value in
// big-endian byte order, KISS-escaped within the frame.
func KISSFrameUint16(cmd byte, value uint16) []byte {
	data := []byte{
		byte(value >> 8),
		byte(value),
	}
	return KISSFrame(cmd, data)
}

// KISSFrameSelectInt builds a KISS frame pair that selects a sub-interface
// before issuing a command: [FEND][CMD_SEL_INT][index][FEND][FEND][cmd][data][FEND].
func KISSFrameSelectInt(cmd byte, index byte, data []byte) []byte {
	escaped := KISSEscape(data)
	frame := make([]byte, 0, 4+2+len(escaped)+1)
	frame = append(frame, KISSFend, KISSCmdSelInt, index, KISSFend)
	frame = append(frame, KISSFend, cmd)
	frame = append(frame, escaped...)
	frame = append(frame, KISSFend)
	return frame
}
