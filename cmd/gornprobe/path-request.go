// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

var errPathRequestTimedOut = errors.New("path request timed out")

type pathRequester interface {
	HasPath([]byte) bool
	RequestPath([]byte) error
}

func waitForProbePath(out io.Writer, ts pathRequester, destHash []byte, timeout float64) error {
	return waitForProbePathAt(out, ts, destHash, timeout, time.Now, time.Sleep)
}

func waitForProbePathAt(out io.Writer, ts pathRequester, destHash []byte, timeout float64, now func() time.Time, sleep func(time.Duration)) error {
	if !ts.HasPath(destHash) {
		if _, err := fmt.Fprintf(out, "Path to %v requested  ", rns.PrettyHex(destHash)); err != nil {
			return err
		}
		if err := ts.RequestPath(destHash); err != nil {
			return fmt.Errorf("Could not request path to %v: %v", rns.PrettyHex(destHash), err)
		}
	}

	spinner := []string{"⢄", "⢂", "⢁", "⡁", "⡈", "⡐", "⡠"}
	index := 0
	deadline := now().Add(time.Duration(timeout * float64(time.Second)))
	for !ts.HasPath(destHash) && now().Before(deadline) {
		sleep(100 * time.Millisecond)
		if _, err := fmt.Fprintf(out, "\b\b%v ", spinner[index]); err != nil {
			return err
		}
		index = (index + 1) % len(spinner)
	}

	if !ts.HasPath(destHash) {
		if _, err := fmt.Fprintf(out, "\r%v\rPath request timed out\n", strings.Repeat(" ", 58)); err != nil {
			return err
		}
		return errPathRequestTimedOut
	}

	return nil
}
