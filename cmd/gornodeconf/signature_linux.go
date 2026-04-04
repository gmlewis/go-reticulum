// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type signatureSetterWriter interface {
	Write([]byte) (int, error)
}

type signatureSetterState struct {
	name      string
	signature []byte
	writer    signatureSetterWriter
}

func (s *signatureSetterState) storeSignature() error {
	data := kissEscape(s.signature)
	command := append([]byte{kissFend, 0x57}, data...)
	command = append(command, kissFend)
	return writeModeCommand(s.writer, command, "sending signature to device")
}
