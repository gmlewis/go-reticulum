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
	"time"

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
	logger             *rns.Logger
	trafficTotals      bool
	discoveredIfaces   bool
	configEntries      bool
	writer             io.Writer
}

type remoteStatusResult struct {
	stats     *rns.InterfaceStatsSnapshot
	linkCount *int
}

func getRemoteStatus(reticulum *rns.Reticulum, p programSetupParams) (*remoteStatusResult, error) {
	w := p.writer
	if w == nil {
		w = os.Stdout
	}
	ts := reticulum.Transport()
	logger := reticulum.Logger()
	targetHash, err := rns.HexToBytes(p.remote)
	if err != nil {
		return nil, fmt.Errorf("invalid destination entered. Check your input")
	}

	if !ts.HasPath(targetHash) {
		if !p.jsonOutput {
			_, _ = fmt.Fprintf(w, "Path to %v requested ", rns.PrettyHexRep(targetHash))
		}
		logger.Debug("Requesting path to %v", rns.PrettyHexRep(targetHash))

		// If we are connecting to a shared instance, we might need to announce
		// ourselves to ensure the other side knows where to send replies.
		// However, RequestPath should be enough if transport is working.

		if err := ts.RequestPath(targetHash); err != nil {
			return nil, fmt.Errorf("path request failed: %w", err)
		}
		timeout := time.Duration(p.remoteTimeout * float64(time.Second))
		if timeout == 0 {
			timeout = 10 * time.Second // Default fallback
		}
		start := time.Now()
		for !ts.HasPath(targetHash) {
			if time.Since(start) > timeout {
				if !p.jsonOutput {
					_, _ = fmt.Print("\r                                                          \r")
					_, _ = fmt.Println("Path request timed out")
				}
				if p.mustExit {
					os.Exit(12)
				}
				return nil, fmt.Errorf("path request timed out")
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	remoteIdentity := rns.RecallIdentity(ts, targetHash)
	if remoteIdentity == nil {
		// Try a bit harder to wait for identity after path response
		time.Sleep(500 * time.Millisecond)
		remoteIdentity = rns.RecallIdentity(ts, targetHash)
	}
	if remoteIdentity == nil {
		return nil, fmt.Errorf("could not recall remote identity for %v", rns.PrettyHexRep(targetHash))
	}

	managementIdentity, err := rns.FromFile(p.managementIdentity, reticulum.Logger())
	if err != nil {
		return nil, fmt.Errorf("could not load management identity from %v: %w", p.managementIdentity, err)
	}

	if !p.jsonOutput {
		_, _ = fmt.Print("\r                                                          \r")
		_, _ = fmt.Print("Establishing link with remote transport instance... ")
	}

	remoteDestination, err := rns.NewDestination(ts, remoteIdentity, rns.DestinationOut, rns.DestinationSingle, "rnstransport", "remote", "management")
	if err != nil {
		return nil, fmt.Errorf("failed to create management destination: %w", err)
	}
	link, err := rns.NewLink(ts, remoteDestination)
	if err != nil {
		return nil, fmt.Errorf("failed to create management link: %w", err)
	}

	resultCh := make(chan *remoteStatusResult, 1)
	errCh := make(chan error, 1)

	link.SetLinkEstablishedCallback(func(l *rns.Link) {
		if !p.jsonOutput {
			_, _ = fmt.Print("\r                                                          \r")
			_, _ = fmt.Print("Sending request... ")
		}
		if err := l.Identify(managementIdentity); err != nil {
			errCh <- fmt.Errorf("failed to identify: %w", err)
			return
		}
		_, err := l.Request("/status", p.linkStats, func(receipt *rns.RequestReceipt) {
			response := receipt.Response
			if respList, ok := response.([]any); ok {
				var res remoteStatusResult
				if len(respList) > 0 {
					res.stats = rns.DecodeInterfaceStats(respList[0])
				}
				if len(respList) > 1 {
					if lc, ok := respList[1].(int); ok {
						res.linkCount = &lc
					} else if lc, ok := respList[1].(int64); ok {
						i := int(lc)
						res.linkCount = &i
					}
				}
				resultCh <- &res
			} else {
				errCh <- fmt.Errorf("unexpected response type: %T", response)
			}
		}, func(receipt *rns.RequestReceipt) {
			errCh <- fmt.Errorf("the remote status request failed. Likely authentication failure")
		}, nil, 0)
		if err != nil {
			errCh <- err
		}
	})

	link.SetLinkClosedCallback(func(l *rns.Link) {
		reason := l.TeardownReason()
		if !p.jsonOutput {
			_, _ = fmt.Print("\r                                                          \r")
			switch reason {
			case rns.TeardownTimeout:
				_, _ = fmt.Println("The link timed out, exiting now")
			case rns.TeardownDestinationClosed:
				_, _ = fmt.Println("The link was closed by the server, exiting now")
			default:
				_, _ = fmt.Println("Link closed unexpectedly, exiting now")
			}
		}
		if p.mustExit {
			os.Exit(10)
		}
		errCh <- fmt.Errorf("link closed")
	})

	select {
	case res := <-resultCh:
		if !p.jsonOutput {
			_, _ = fmt.Print("\r                                                          \r")
		}
		return res, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(30 * time.Second): // Overall request timeout
		return nil, fmt.Errorf("remote status request timed out")
	}
}

// programSetup implements the core logic of rnstatus's program_setup()
// function. It initializes Reticulum, retrieves interface statistics,
// and renders the output.
func programSetup(p programSetupParams) int {
	w := p.writer
	if w == nil {
		w = os.Stdout
	}
	if p.logger == nil {
		p.logger = rns.NewLogger()
	}
	logger := p.logger
	if p.verbosity > 0 {
		level := rns.LogInfo
		if p.verbosity > 1 {
			level = rns.LogDebug
		}
		if p.verbosity > 2 {
			level = rns.LogExtreme
		}
		logger.SetLogLevel(level)
	}

	var reticulum *rns.Reticulum
	if p.rnsInstance != nil {
		reticulum = p.rnsInstance
		p.mustExit = false
	} else {
		ts := rns.NewTransportSystem(logger)
		// Discovery logic in Python rnstatus.py creates InterfaceDiscovery with discover_interfaces=False
		// which means it only reads from storage and doesn't start new interface scanning.
		// In our Go port, NewReticulum handles the configuration.
		r, err := rns.NewReticulumWithLogger(ts, p.configDir, logger)
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
				logger.Warning("Error closing Reticulum: %v", err)
			}
		}()
	}

	if p.discoveredIfaces {
		discovery := rns.NewInterfaceDiscovery(reticulum)
		ifs, err := discovery.ListDiscoveredInterfaces(false, false)
		if err != nil {
			_, _ = fmt.Fprintf(w, "Could not list discovered interfaces: %v\n", err)
			if p.mustExit {
				return 3
			}
			return 0
		}

		_, _ = fmt.Fprintln(w)
		if p.jsonOutput {
			if err := renderDiscoveredJSON(w, ifs); err != nil {
				_, _ = fmt.Fprintf(w, "JSON encoding error: %v\n", err)
			}
		} else {
			var filtered []rns.DiscoveredInterface
			for _, i := range ifs {
				if p.nameFilter == "" || strings.Contains(strings.ToLower(i.Name), strings.ToLower(p.nameFilter)) {
					filtered = append(filtered, i)
				}
			}

			if p.configEntries { // -D flag
				renderDiscoveredInterfaceDetails(w, filtered)
			} else { // -d flag
				renderDiscoveredInterfaces(w, filtered)
			}
		}

		if p.mustExit {
			return 0
		}
		return 0
	}

	var linkCount *int
	if p.linkStats {
		if lc, err := reticulum.LinkCount(); err == nil {
			linkCount = &lc
		}
	}

	var stats *rns.InterfaceStatsSnapshot
	if p.remote != "" {
		res, err := getRemoteStatus(reticulum, p)
		if err != nil {
			_, _ = fmt.Fprintln(w, err.Error())
			if p.mustExit {
				os.Exit(20)
			}
			return 0
		}
		stats = res.stats
		linkCount = res.linkCount
	} else {
		s, err := reticulum.InterfaceStats()
		if err == nil {
			stats = s
		}
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
