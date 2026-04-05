// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"

	"github.com/gmlewis/go-reticulum/rns"
)

type pathDropper interface {
	InvalidatePath([]byte) bool
	InvalidatePathsViaNextHop([]byte) int
}

func doDrop(out io.Writer, ts pathDropper, destHash []byte) error {
	if ts.InvalidatePath(destHash) {
		_, err := fmt.Fprintf(out, "Dropped path to %x\n", destHash)
		return err
	}
	_, err := fmt.Fprintf(out, "Unable to drop path to %x. Does it exist?\n", destHash)
	if err != nil {
		return err
	}
	return fmt.Errorf("Unable to drop path to %x. Does it exist?", destHash)
}

func doDropVia(out io.Writer, ts pathDropper, destHash []byte) error {
	count := ts.InvalidatePathsViaNextHop(destHash)
	if count > 0 {
		_, err := fmt.Fprintf(out, "Dropped all paths via %x\n", destHash)
		return err
	}
	_, err := fmt.Fprintf(out, "Unable to drop paths via %x. Does the transport instance exist?\n", destHash)
	if err != nil {
		return err
	}
	return fmt.Errorf("Unable to drop paths via %x. Does the transport instance exist?", destHash)
}

func dropDestinationBytes(input string) ([]byte, error) {
	return parseHash(input)
}

func _dropToTransportSystem(ts *rns.TransportSystem, destHash []byte) bool {
	return ts.InvalidatePath(destHash)
}
