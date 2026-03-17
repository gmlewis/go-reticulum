// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
)

// programSetupParams holds all the parameters for programSetup,
// matching the Python program_setup() function signature.
type programSetupParams struct {
	configDir          string
	dispAll            bool
	verbosity          int
	nameFilter         string
	jsonOutput         bool
	announceStats      bool
	linkStats          bool
	sorting            string
	sortReverse        bool
	remote             string
	managementIdentity string
	remoteTimeout      float64
	mustExit           bool
	rnsInstance        *rns.Reticulum
	trafficTotals      bool
	discoveredIfaces   bool
	configEntries      bool
	writer             io.Writer
}

// programSetup implements the core logic of rnstatus's program_setup()
// function. It initializes Reticulum, retrieves interface statistics,
// and renders the output.
func programSetup(p programSetupParams) int {
	w := p.writer
	if w == nil {
		w = os.Stdout
	}

	var reticulum *rns.Reticulum
	if p.rnsInstance != nil {
		reticulum = p.rnsInstance
		p.mustExit = false
	} else {
		ts := rns.NewTransportSystem()
		r, err := rns.NewReticulum(ts, p.configDir)
		if err != nil {
			_, _ = fmt.Fprintln(w, "No shared RNS instance available to get status from")
			if p.mustExit {
				return 1
			}
			return 0
		}
		reticulum = r
		defer func() {
			if err := reticulum.Close(); err != nil {
				_, _ = fmt.Fprintf(w, "Error closing Reticulum: %v\n", err)
			}
		}()
	}

	var linkCount *int
	if p.linkStats {
		if lc, err := reticulum.LinkCount(); err == nil {
			linkCount = &lc
		}
	}

	stats, err := reticulum.InterfaceStats()
	if err != nil {
		stats = nil
	}

	if stats == nil {
		if p.remote == "" {
			_, _ = fmt.Fprintln(w, "Could not get RNS status")
		} else {
			_, _ = fmt.Fprintf(w, "Could not get RNS status from remote transport instance %v\n", p.remote)
		}
		if p.mustExit {
			return 2
		}
		return 0
	}

	if p.jsonOutput {
		if err := renderJSON(w, stats); err != nil {
			_, _ = fmt.Fprintf(w, "JSON encoding error: %v\n", err)
		}
		return 0
	}

	ifaces := stats.Interfaces
	if p.sorting != "" {
		sortInterfaces(ifaces, p.sorting, p.sortReverse)
	}

	for _, ifstat := range ifaces {
		if shouldDisplayInterface(ifstat, p.dispAll, p.nameFilter) {
			_, _ = fmt.Fprintln(w)
			renderInterface(w, ifstat, p.announceStats)
		}
	}

	lstr := ""
	if linkCount != nil && p.linkStats {
		hasTransport := len(stats.TransportID) > 0
		lstr = linkStatsString(linkCount, hasTransport)
	}

	if p.trafficTotals {
		renderTotals(w, stats)
	}

	renderTransportFooter(w, stats, lstr)
	_, _ = fmt.Fprintln(w)

	return 0
}

// shouldDisplayInterface returns true if the given interface should be
// displayed based on the dispAll flag and nameFilter. This mirrors the
// Python filtering logic in program_setup().
func shouldDisplayInterface(ifstat rns.InterfaceStat, dispAll bool, nameFilter string) bool {
	name := ifstat.Name

	isHidden :=
		strings.HasPrefix(name, "LocalInterface[") ||
			strings.HasPrefix(name, "TCPInterface[Client") ||
			strings.HasPrefix(name, "BackboneInterface[Client on") ||
			strings.HasPrefix(name, "AutoInterfacePeer[") ||
			strings.HasPrefix(name, "WeaveInterfacePeer[") ||
			strings.HasPrefix(name, "I2PInterfacePeer[Connected peer") ||
			(strings.HasPrefix(name, "I2PInterface[") &&
				ifstat.I2PConnectable != nil && !*ifstat.I2PConnectable)

	if !dispAll && isHidden {
		return false
	}

	// Non-connectable I2PInterface is always hidden even with dispAll.
	if strings.HasPrefix(name, "I2PInterface[") &&
		ifstat.I2PConnectable != nil && !*ifstat.I2PConnectable {
		return false
	}

	if nameFilter != "" &&
		!strings.Contains(strings.ToLower(name), strings.ToLower(nameFilter)) {
		return false
	}

	return true
}
