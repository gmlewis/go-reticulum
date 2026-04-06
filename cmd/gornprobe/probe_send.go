// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"io"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

func sleepBetweenProbes(sent int, wait float64, sleep func(time.Duration)) {
	if sent > 0 {
		sleep(time.Duration(wait * float64(time.Second)))
	}
}

func waitForProbeReceiptAt(out io.Writer, receipt *rns.PacketReceipt, timeout float64, now func() time.Time, sleep func(time.Duration)) bool {
	spinner := []string{"⢄", "⢂", "⢁", "⡁", "⡈", "⡐", "⡠"}
	index := 0
	deadline := now().Add(time.Duration(timeout * float64(time.Second)))
	for now().Before(deadline) {
		if receipt.Status == rns.ReceiptDelivered {
			return true
		}
		sleep(100 * time.Millisecond)
		_, _ = io.WriteString(out, "\b\b"+spinner[index]+" ")
		index = (index + 1) % len(spinner)
	}
	if receipt.Status == rns.ReceiptDelivered {
		return true
	}
	_, _ = io.WriteString(out, "\r"+strings.Repeat(" ", 64)+"\rProbe timed out\n")
	return false
}
