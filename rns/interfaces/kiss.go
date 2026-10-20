// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// Constants defining the framing and escape markers of the KISS (Keep It
// Simple, Stupid) protocol.
const (
	// KISSFend is the frame-end marker that starts or ends a packet.
	KISSFend = 0xC0
	// KISSFesc is the frame-escape byte.
	KISSFesc = 0xDB
	// KISSTfend is the transposed frame-end byte substituted for a literal
	// Fend in the payload.
	KISSTfend = 0xDC
	// KISSTfesc is the transposed frame-escape byte substituted for a literal
	// Fesc in the payload.
	KISSTfesc = 0xDD
	// KISSCmdData marks a frame carrying routable network data rather than a
	// control command.
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

	// KISS command bytes for device reset and framebuffer/display access
	// (RNS/Interfaces/RNodeInterface.py, KISS class). The detection/firmware
	// constants (KISSCmdFwVersion, KISSCmdPlatform, KISSCmdMcu, KISSDetectReq,
	// KISSDetectResp) already live in rnode-multi-spawn.go.
	KISSCmdReset    = 0x55
	KISSCmdFBExt    = 0x41
	KISSCmdFBRead   = 0x42
	KISSCmdFBWrite  = 0x43
	KISSCmdDispRead = 0x66

	// Radio state constants.
	RadioStateOff = 0x00
	RadioStateOn  = 0x01
	RadioStateAsk = 0xFF
)

// KISSEscape applies KISS byte stuffing to data, so embedded boundary markers
// survive transmission over a raw serial link.
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

// KISSUnescape reverses KISS byte stuffing, restoring the original payload.
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
