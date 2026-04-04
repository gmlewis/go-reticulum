// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type serialDiscoveryState struct {
	root     string
	readDir  func(string) ([]os.DirEntry, error)
	readLink func(string) (string, error)
}

func newSerialDiscoveryState() *serialDiscoveryState {
	return &serialDiscoveryState{
		root:     "/dev/serial/by-id",
		readDir:  os.ReadDir,
		readLink: os.Readlink,
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
		name   string
		port   string
		likely bool
	}

	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		name := entry.Name()
		target, err := s.readLink(filepath.Join(s.root, name))
		if err != nil {
			continue
		}
		resolved := filepath.Clean(filepath.Join(s.root, target))
		if !strings.HasPrefix(resolved, "/dev/") {
			continue
		}
		portName := filepath.Base(resolved)
		if !strings.HasPrefix(portName, "ttyACM") && !strings.HasPrefix(portName, "ttyUSB") {
			continue
		}
		candidates = append(candidates, candidate{
			name:   name,
			port:   resolved,
			likely: strings.Contains(strings.ToUpper(name), "JTAG"),
		})
	}

	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].name < candidates[j].name })

	ports := make([]string, 0, len(candidates))
	likelyPorts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ports = append(ports, candidate.port)
		if candidate.likely {
			likelyPorts = append(likelyPorts, candidate.port)
		}
	}

	switch {
	case len(likelyPorts) == 1:
		return likelyPorts[0], ports, nil
	case len(ports) == 1:
		return ports[0], ports, nil
	default:
		return "", ports, nil
	}
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
		return "", fmt.Errorf("no serial port specified; no likely RNode device was found under /dev/serial/by-id. Pass the port explicitly, for example /dev/serial/by-id/... or /dev/ttyACM0")
	}
	return "", fmt.Errorf("no serial port specified; multiple likely RNode devices were found: %v. Pass the port explicitly", strings.Join(candidates, ", "))
}
