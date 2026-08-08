// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.
//
//go:build freebsd

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type serialDiscoveryState struct {
	root    string
	readDir func(string) ([]os.DirEntry, error)
}

func newSerialDiscoveryState() *serialDiscoveryState {
	return &serialDiscoveryState{
		root:    "/dev",
		readDir: os.ReadDir,
	}
}

func defaultDiscoverRnodeSerialPort() (string, []string, error) {
	return newSerialDiscoveryState().discover()
}

func (rt cliRuntime) discoverRnodeSerialPort() (string, []string, error) {
	discover := rt.discoverPort
	if discover == nil {
		discover = defaultDiscoverRnodeSerialPort
	}
	return discover()
}

func (s *serialDiscoveryState) discover() (string, []string, error) {
	entries, err := s.readDir(s.root)
	if err != nil {
		return "", nil, err
	}

	type candidate struct {
		port  string
		score int
	}

	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		score, ok := freebsdSerialCandidateScore(entry.Name())
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{
			port:  filepath.Join(s.root, entry.Name()),
			score: score,
		})
	}

	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].port < candidates[j].port })

	ports := make([]string, 0, len(candidates))
	bestPort := ""
	bestScore := -1
	bestScoreCount := 0
	for _, candidate := range candidates {
		ports = append(ports, candidate.port)
		switch {
		case candidate.score > bestScore:
			bestPort = candidate.port
			bestScore = candidate.score
			bestScoreCount = 1
		case candidate.score == bestScore:
			bestScoreCount++
		}
	}

	switch {
	case len(ports) == 1:
		return ports[0], ports, nil
	case bestScoreCount == 1:
		return bestPort, ports, nil
	default:
		return "", ports, nil
	}
}

// freebsdSerialCandidateScore scores /dev entries as likely RNode serial
// ports. FreeBSD names USB-serial callout devices /dev/cuaU* and their
// incoming counterparts /dev/ttyU*; the callout (cuaU) is preferred for
// opening. Unlike linux there is no /dev/serial/by-id symlink tree, so a flat
// /dev scan (the darwin model) is used.
func freebsdSerialCandidateScore(name string) (int, bool) {
	lowerName := strings.ToLower(name)

	prefixScore := -1
	switch {
	case strings.HasPrefix(lowerName, "cuau"):
		prefixScore = 10
	case strings.HasPrefix(lowerName, "ttyu"):
		prefixScore = 0
	default:
		return 0, false
	}

	// "jtag" appears in some RNode/adapter device labels; treat it as a mild
	// preference so a dedicated JTAG/programming adapter outranks a generic
	// ttyU when both are present.
	tokenScore := 0
	switch {
	case strings.Contains(lowerName, "jtag"):
		tokenScore = 30
	}

	return tokenScore + prefixScore, true
}

func resolveLivePort(port string, opts options) (string, error) {
	return newRuntime().resolveLivePort(port, opts)
}

func (rt cliRuntime) resolveLivePort(port string, opts options) (string, error) {
	if port != "" {
		return port, nil
	}
	if !(opts.sign || opts.firmwareHash != "" || opts.getTargetFirmwareHash || opts.getFirmwareHash) {
		return "", nil
	}

	detected, candidates, err := rt.discoverRnodeSerialPort()
	if err != nil {
		return "", err
	}
	if detected != "" {
		return detected, nil
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no serial port specified; no likely RNode device was found under /dev. Pass the port explicitly, for example /dev/cuaU0 or /dev/ttyU0")
	}
	return "", fmt.Errorf("no serial port specified; multiple likely RNode devices were found: %v. Pass the port explicitly", strings.Join(candidates, ", "))
}
