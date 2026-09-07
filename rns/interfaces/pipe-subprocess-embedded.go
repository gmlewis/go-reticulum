// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build embedded || pocket_communicator || pocket_hub

package interfaces

import (
	"fmt"
	"time"
)

const (
	PipeBitrateGuess        = 1 * 1000 * 1000
	PipeHWMTU               = 1064
	PipeDefaultRespawnDelay = 5 * time.Second
)

// NewPipeSubprocessInterface returns an error on embedded targets where subprocesses are not supported.
func NewPipeSubprocessInterface(name, command string, respawnDelay time.Duration, handler InboundHandler) (Interface, error) {
	return nil, fmt.Errorf("pipe subprocess interface not supported on embedded platform")
}
