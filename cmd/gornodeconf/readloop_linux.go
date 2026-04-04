// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

const (
	rnodeKISSCommandUnknown   = 0xfe
	rnodeKISSCommandFrequency = 0x01
	rnodeKISSCommandBandwidth = 0x02
	rnodeKISSCommandFWVersion = 0x50
	rnodeKISSCommandROMRead   = 0x51
	rnodeKISSCommandDevHash   = 0x56
	rnodeKISSCommandCFGRead   = 0x6d
	rnodeKISSCommandData      = 0x00

	rnodeReadLoopFrameLimit = 1024
)

type rnodeReadLoopFrame struct {
	command byte
	payload []byte
}

type rnodeReadLoopState struct {
	inFrame       bool
	escape        bool
	command       byte
	dataBuffer    []byte
	commandBuffer []byte
	rFrequency    int
	rBandwidth    int
	majorVersion  byte
	minorVersion  byte
	deviceHash    []byte
}

func newRnodeReadLoopState() *rnodeReadLoopState {
	return &rnodeReadLoopState{command: rnodeKISSCommandUnknown}
}

func (s *rnodeReadLoopState) feedByte(b byte) (rnodeReadLoopFrame, bool) {
	if s.inFrame && b == kissFend && s.isPayloadCommand() {
		frame := rnodeReadLoopFrame{
			command: s.command,
			payload: append([]byte(nil), s.dataBuffer...),
		}
		s.resetFrame()
		return frame, true
	}

	if b == kissFend {
		s.inFrame = true
		s.escape = false
		s.command = rnodeKISSCommandUnknown
		s.dataBuffer = s.dataBuffer[:0]
		s.commandBuffer = s.commandBuffer[:0]
		return rnodeReadLoopFrame{}, false
	}

	if !s.inFrame || len(s.dataBuffer) >= rnodeReadLoopFrameLimit {
		return rnodeReadLoopFrame{}, false
	}

	if len(s.dataBuffer) == 0 && s.command == rnodeKISSCommandUnknown {
		s.command = b
		return rnodeReadLoopFrame{}, false
	}

	if s.command != rnodeKISSCommandROMRead && s.command != rnodeKISSCommandCFGRead && s.command != rnodeKISSCommandData && s.command != rnodeKISSCommandFrequency && s.command != rnodeKISSCommandBandwidth && s.command != rnodeKISSCommandFWVersion && s.command != rnodeKISSCommandDevHash {
		return rnodeReadLoopFrame{}, false
	}

	if b == kissFesc {
		s.escape = true
		return rnodeReadLoopFrame{}, false
	}

	if s.escape {
		s.escape = false
		b = decodeKISSEscapedByte(b)
	}

	if s.command == rnodeKISSCommandFrequency || s.command == rnodeKISSCommandBandwidth || s.command == rnodeKISSCommandFWVersion || s.command == rnodeKISSCommandDevHash {
		s.commandBuffer = append(s.commandBuffer, b)
		s.applyCommandBuffer()
		return rnodeReadLoopFrame{}, false
	}

	s.dataBuffer = append(s.dataBuffer, b)
	return rnodeReadLoopFrame{}, false
}

func (s *rnodeReadLoopState) applyCommandBuffer() {
	switch s.command {
	case rnodeKISSCommandFrequency:
		if len(s.commandBuffer) == 4 {
			s.rFrequency = int(s.commandBuffer[0])<<24 | int(s.commandBuffer[1])<<16 | int(s.commandBuffer[2])<<8 | int(s.commandBuffer[3])
		}
	case rnodeKISSCommandBandwidth:
		if len(s.commandBuffer) == 4 {
			s.rBandwidth = int(s.commandBuffer[0])<<24 | int(s.commandBuffer[1])<<16 | int(s.commandBuffer[2])<<8 | int(s.commandBuffer[3])
		}
	case rnodeKISSCommandFWVersion:
		if len(s.commandBuffer) == 2 {
			s.majorVersion = s.commandBuffer[0]
			s.minorVersion = s.commandBuffer[1]
		}
	case rnodeKISSCommandDevHash:
		if len(s.commandBuffer) == 32 {
			s.deviceHash = append([]byte(nil), s.commandBuffer...)
		}
	}
}

func (s *rnodeReadLoopState) isPayloadCommand() bool {
	switch s.command {
	case rnodeKISSCommandROMRead, rnodeKISSCommandCFGRead, rnodeKISSCommandData:
		return true
	default:
		return false
	}
}

func (s *rnodeReadLoopState) resetFrame() {
	s.inFrame = false
	s.escape = false
	s.command = rnodeKISSCommandUnknown
	s.dataBuffer = s.dataBuffer[:0]
	s.commandBuffer = s.commandBuffer[:0]
}

func (s *rnodeReadLoopState) resetForIdleTimeout() {
	s.inFrame = false
	s.escape = false
	s.command = rnodeKISSCommandUnknown
	s.dataBuffer = s.dataBuffer[:0]
}

func (s *rnodeReadLoopState) idleTimeoutExpired(nowMs, lastReadMs, timeoutMs int) bool {
	if len(s.dataBuffer) > 0 && nowMs-lastReadMs > timeoutMs {
		s.resetForIdleTimeout()
		return true
	}
	return false
}

func (s *rnodeReadLoopState) shutdownCleanup() {
	s.resetFrame()
}

func decodeKISSEscapedByte(b byte) byte {
	if b == kissTfend {
		return kissFend
	}
	if b == kissTfesc {
		return kissFesc
	}
	return b
}
