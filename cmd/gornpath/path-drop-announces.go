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

type announceDropper interface {
	DropAnnounceQueues() int
}

func doDropAnnounces(out io.Writer, ts announceDropper) error {
	if _, err := fmt.Fprintln(out, "Dropping announce queues on all interfaces..."); err != nil {
		return err
	}
	ts.DropAnnounceQueues()
	return nil
}

func _dropAnnouncesToTransportSystem(ts *rns.TransportSystem) int {
	return ts.DropAnnounceQueues()
}
