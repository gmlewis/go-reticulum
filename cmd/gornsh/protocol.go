// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

const (
	msgMagic        = 0xac
	protocolVersion = 1
)

const (
	msgTypeNoop          = 0
	msgTypeWindowSize    = 2
	msgTypeExecute       = 3
	msgTypeStreamData    = 4
	msgTypeVersionInfo   = 5
	msgTypeError         = 6
	msgTypeCommandExited = 7
)

const (
	streamIDStdin  = 0
	streamIDStdout = 1
	streamIDStderr = 2
)

func makeMsgType(value int) int {
	return ((msgMagic << 8) & 0xff00) | (value & 0x00ff)
}

type noopMessage struct{}

func (m noopMessage) pack() ([]byte, error) {
	return []byte{}, nil
}

func (m *noopMessage) GetMsgType() uint16 { return uint16(makeMsgType(msgTypeNoop)) }
func (m *noopMessage) Pack() ([]byte, error) {
	return m.pack()
}
func (m *noopMessage) Unpack(data []byte) error {
	return nil
}

type windowSizeMessage struct {
	Rows *int
	Cols *int
	HPix *int
	VPix *int
}

func (m *windowSizeMessage) GetMsgType() uint16 { return uint16(makeMsgType(msgTypeWindowSize)) }
func (m *windowSizeMessage) Pack() ([]byte, error) {
	return m.pack()
}
func (m *windowSizeMessage) Unpack(data []byte) error {
	return m.unpack(data)
}

func (m windowSizeMessage) pack() ([]byte, error) {
	return msgpack.Pack([]any{m.Rows, m.Cols, m.HPix, m.VPix})
}

func (m *windowSizeMessage) unpack(raw []byte) error {
	data, err := msgpack.Unpack(raw)
	if err != nil {
		return err
	}
	parts, ok := data.([]any)
	if !ok || len(parts) != 4 {
		return fmt.Errorf("invalid window size payload: %#v", data)
	}
	m.Rows = toOptionalIntPtr(parts[0])
	m.Cols = toOptionalIntPtr(parts[1])
	m.HPix = toOptionalIntPtr(parts[2])
	m.VPix = toOptionalIntPtr(parts[3])
	return nil
}

type executeCommandMessage struct {
	CommandLine []string
	PipeStdin   bool
	PipeStdout  bool
	PipeStderr  bool
	TCFlags     any
	Term        *string
	Rows        *int
	Cols        *int
	HPix        *int
	VPix        *int
}

func (m *executeCommandMessage) GetMsgType() uint16 { return uint16(makeMsgType(msgTypeExecute)) }
func (m *executeCommandMessage) Pack() ([]byte, error) {
	return m.pack()
}
func (m *executeCommandMessage) Unpack(data []byte) error {
	return m.unpack(data)
}

type streamDataMessage struct {
	StreamID   int
	Data       []byte
	EOF        bool
	Compressed bool
}

func (m streamDataMessage) pack() ([]byte, error) {
	return msgpack.Pack([]any{m.StreamID, m.Data, m.EOF, m.Compressed})
}

func (m *streamDataMessage) unpack(raw []byte) error {
	data, err := msgpack.Unpack(raw)
	if err != nil {
		return err
	}
	parts, ok := data.([]any)
	if !ok || len(parts) != 4 {
		return fmt.Errorf("invalid stream data payload: %#v", data)
	}
	m.StreamID, _ = toInt(parts[0])
	if b, ok := toBytes(parts[1]); ok {
		m.Data = b
	}
	m.EOF = toBool(parts[2])
	m.Compressed = toBool(parts[3])
	return nil
}

func (m *streamDataMessage) GetMsgType() uint16 { return uint16(makeMsgType(msgTypeStreamData)) }
func (m *streamDataMessage) Pack() ([]byte, error) {
	return m.pack()
}
func (m *streamDataMessage) Unpack(data []byte) error {
	return m.unpack(data)
}

func (m executeCommandMessage) pack() ([]byte, error) {
	cmdline := make([]any, 0, len(m.CommandLine))
	for _, value := range m.CommandLine {
		cmdline = append(cmdline, value)
	}
	return msgpack.Pack([]any{cmdline, m.PipeStdin, m.PipeStdout, m.PipeStderr, m.TCFlags, m.Term, m.Rows, m.Cols, m.HPix, m.VPix})
}

func (m *executeCommandMessage) unpack(raw []byte) error {
	data, err := msgpack.Unpack(raw)
	if err != nil {
		return err
	}
	parts, ok := data.([]any)
	if !ok || len(parts) != 10 {
		return fmt.Errorf("invalid execute command payload: %#v", data)
	}
	m.CommandLine = toStringSlice(parts[0])
	m.PipeStdin = toBool(parts[1])
	m.PipeStdout = toBool(parts[2])
	m.PipeStderr = toBool(parts[3])
	m.TCFlags = parts[4]
	m.Term = toOptionalStringPtr(parts[5])
	m.Rows = toOptionalIntPtr(parts[6])
	m.Cols = toOptionalIntPtr(parts[7])
	m.HPix = toOptionalIntPtr(parts[8])
	m.VPix = toOptionalIntPtr(parts[9])
	return nil
}

type versionInfoMessage struct {
	SoftwareVersion string
	ProtocolVersion int
}

func (m *versionInfoMessage) GetMsgType() uint16 { return uint16(makeMsgType(msgTypeVersionInfo)) }
func (m *versionInfoMessage) Pack() ([]byte, error) {
	return m.pack()
}
func (m *versionInfoMessage) Unpack(data []byte) error {
	return m.unpack(data)
}

func (m versionInfoMessage) pack() ([]byte, error) {
	version := m.ProtocolVersion
	if version == 0 {
		version = protocolVersion
	}
	return msgpack.Pack([]any{m.SoftwareVersion, version})
}

func (m *versionInfoMessage) unpack(raw []byte) error {
	data, err := msgpack.Unpack(raw)
	if err != nil {
		return err
	}
	parts, ok := data.([]any)
	if !ok || len(parts) != 2 {
		return fmt.Errorf("invalid version info payload: %#v", data)
	}
	m.SoftwareVersion, _ = parts[0].(string)
	m.ProtocolVersion, _ = toInt(parts[1])
	return nil
}

type errorMessage struct {
	Message string
	Fatal   bool
	Data    any
}

func (m *errorMessage) GetMsgType() uint16 { return uint16(makeMsgType(msgTypeError)) }
func (m *errorMessage) Pack() ([]byte, error) {
	return m.pack()
}
func (m *errorMessage) Unpack(data []byte) error {
	return m.unpack(data)
}

func (m errorMessage) pack() ([]byte, error) {
	return msgpack.Pack([]any{m.Message, m.Fatal, m.Data})
}

func (m *errorMessage) unpack(raw []byte) error {
	data, err := msgpack.Unpack(raw)
	if err != nil {
		return err
	}
	parts, ok := data.([]any)
	if !ok || len(parts) != 3 {
		return fmt.Errorf("invalid error payload: %#v", data)
	}
	m.Message, _ = parts[0].(string)
	m.Fatal = toBool(parts[1])
	m.Data = parts[2]
	return nil
}

type commandExitedMessage struct {
	ReturnCode int
}

func (m *commandExitedMessage) GetMsgType() uint16 { return uint16(makeMsgType(msgTypeCommandExited)) }
func (m *commandExitedMessage) Pack() ([]byte, error) {
	return m.pack()
}
func (m *commandExitedMessage) Unpack(data []byte) error {
	return m.unpack(data)
}

func registerProtocolMessageTypes(channel *rns.Channel) {
	channel.RegisterMessageType(uint16(makeMsgType(msgTypeNoop)), func() rns.Message { return &noopMessage{} })
	channel.RegisterMessageType(uint16(makeMsgType(msgTypeWindowSize)), func() rns.Message { return &windowSizeMessage{} })
	channel.RegisterMessageType(uint16(makeMsgType(msgTypeExecute)), func() rns.Message { return &executeCommandMessage{} })
	channel.RegisterMessageType(uint16(makeMsgType(msgTypeStreamData)), func() rns.Message { return &streamDataMessage{} })
	channel.RegisterMessageType(uint16(makeMsgType(msgTypeVersionInfo)), func() rns.Message { return &versionInfoMessage{} })
	channel.RegisterMessageType(uint16(makeMsgType(msgTypeError)), func() rns.Message { return &errorMessage{} })
	channel.RegisterMessageType(uint16(makeMsgType(msgTypeCommandExited)), func() rns.Message { return &commandExitedMessage{} })
}

func (m commandExitedMessage) pack() ([]byte, error) {
	return msgpack.Pack(m.ReturnCode)
}

func (m *commandExitedMessage) unpack(raw []byte) error {
	data, err := msgpack.Unpack(raw)
	if err != nil {
		return err
	}
	value, ok := toInt(data)
	if !ok {
		return fmt.Errorf("invalid command exited payload: %#v", data)
	}
	m.ReturnCode = value
	return nil
}

func toOptionalIntPtr(value any) *int {
	if value == nil {
		return nil
	}
	v, ok := toInt(value)
	if !ok {
		return nil
	}
	out := v
	return &out
}

func toOptionalStringPtr(value any) *string {
	v, ok := value.(string)
	if !ok {
		return nil
	}
	out := v
	return &out
}

func toStringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		if strings, ok := value.([]string); ok {
			return append([]string{}, strings...)
		}
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			continue
		}
		out = append(out, text)
	}
	return out
}

func toBool(value any) bool {
	b, ok := value.(bool)
	if ok {
		return b
	}
	return false
}
