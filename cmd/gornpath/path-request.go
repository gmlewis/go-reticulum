// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

type pathRequester interface {
	HasPath([]byte) bool
	RequestPath([]byte) error
	GetPathEntry([]byte) *rns.PathInfo
}

func doRequest(out io.Writer, ts pathRequester, destHash []byte, timeout float64) error {
	return doRequestAt(out, ts, destHash, timeout, time.Now, time.Sleep)
}

func doRequestAt(out io.Writer, ts pathRequester, destHash []byte, timeout float64, now func() time.Time, sleep func(time.Duration)) error {
	if !ts.HasPath(destHash) {
		if _, err := fmt.Fprintf(out, "Path to %x requested  ", destHash); err != nil {
			return err
		}
		if err := ts.RequestPath(destHash); err != nil {
			return fmt.Errorf("Could not request path to %x: %v", destHash, err)
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

	if ts.HasPath(destHash) {
		entry := ts.GetPathEntry(destHash)
		if entry == nil {
			return fmt.Errorf("Error: Invalid path data returned")
		}
		plural := "s"
		if entry.Hops == 1 {
			plural = " "
		}
		if _, err := fmt.Fprintf(out, "\rPath found, destination %x is %v hop%v away via %x on %v\n", destHash, entry.Hops, plural, entry.NextHop, entry.Interface.Name()); err != nil {
			return err
		}
		return nil
	}

	if _, err := fmt.Fprintf(out, "\r%v\rPath not found\n", strings.Repeat(" ", 55)); err != nil {
		return err
	}
	return fmt.Errorf("Path not found")
}
