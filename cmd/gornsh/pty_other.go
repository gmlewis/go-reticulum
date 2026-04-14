// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !linux && !darwin

package main

import (
	"fmt"

	"github.com/gmlewis/go-reticulum/rns"
)

type ptyPair struct{}

func (rt *runtimeT) startPTYSessionCommand(sender messageSender, commandLine []string, remoteIdentity *rns.Identity, execute *executeCommandMessage) (*activeCommand, error) {
	return nil, fmt.Errorf("PTY execution is not supported on this platform")
}

func openPTY() (*ptyPair, error) {
	return nil, fmt.Errorf("PTY execution is not supported on this platform")
}

func termiosFromTCFlags(raw any) (any, error) {
	return nil, fmt.Errorf("PTY execution is not supported on this platform")
}
