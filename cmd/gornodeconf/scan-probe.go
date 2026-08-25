// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"
)

// probeBaudRates is the set of baud rates tried when probing a port for an
// RNode, covering the common RNode serial speeds.
var probeBaudRates = []int{115200, 57600, 38400, 19200, 9600}

// runScan lists all serial ports on the system and probes each for an RNode
// detect response at the default baud (115200). This is the "which /dev port is
// my RNode on?" diagnostic — the question that comes up every time a radio is
// plugged into a new machine.
func runScan(rt cliRuntime) error {
	ports := listSerialPorts()
	if len(ports) == 0 {
		fmt.Println("No serial ports found.")
		fmt.Println("Check that the device is plugged in with a DATA cable (not a charge-only cable).")
		return nil
	}
	fmt.Printf("Scanning %d serial port(s) for RNode devices at %d baud...\n\n", len(ports), rnodeBaudRate)
	found := false
	for _, port := range ports {
		detected, err := probePortAtBaud(rt, port, rnodeBaudRate)
		if err != nil {
			fmt.Printf("  %-45s error: %v\n", port, err)
			continue
		}
		if detected {
			fmt.Printf("  %-45s RNode detected\n", port)
			found = true
		} else {
			fmt.Printf("  %-45s no response\n", port)
		}
	}
	if !found {
		fmt.Println("\nNo RNode found on any port.")
		fmt.Println("Possible causes:")
		fmt.Println("  - The USB cable is power-only (no data pair) — try a different cable.")
		fmt.Println("  - The radio is not powered or not in host-controlled (Normal) mode.")
		fmt.Println("  - The radio responds at a non-standard baud — try: gornodeconf --probe <port>")
	} else {
		fmt.Println("\nRNode found! Use the port path above in your Reticulum config:")
		fmt.Println("  port = <the port path that reported 'RNode detected'>")
	}
	return nil
}

// runProbe probes a specific serial port at multiple baud rates for an RNode
// detect response. This is the "is my RNode even responding, and at what baud?"
// diagnostic — the question that distinguishes a baud mismatch from a
// wiring/cable/power problem.
func runProbe(rt cliRuntime, port string) error {
	fmt.Printf("Probing %s at %d baud rate(s)...\n\n", port, len(probeBaudRates))
	for _, baud := range probeBaudRates {
		detected, err := probePortAtBaud(rt, port, baud)
		if err != nil {
			fmt.Printf("  %6d baud: error: %v\n", baud, err)
			continue
		}
		if detected {
			fmt.Printf("  %6d baud: RNode detected!\n", baud)
			fmt.Printf("\nUse this port and baud in your Reticulum config:\n")
			fmt.Printf("  port = %s\n", port)
			if baud != rnodeBaudRate {
				fmt.Printf("  speed = %d\n", baud)
			}
			return nil
		}
		fmt.Printf("  %6d baud: no response\n", baud)
	}
	fmt.Printf("\nNo RNode response on %s at any baud rate.\n", port)
	fmt.Println("Possible causes:")
	fmt.Println("  - The USB cable is power-only (no data pair) — try a different cable.")
	fmt.Println("  - The radio is not powered or not in host-controlled (Normal) mode.")
	fmt.Println("  - TX/RX wiring is swapped (for UART-attached radios).")
	fmt.Println("  - On Raspberry Pi: the GPIO UART pins are not muxed to the UART function.")
	return nil
}

// probePortAtBaud opens a serial port at the given baud, sends a KISS detect
// frame, reads the response with a timeout, and reports whether an RNode
// detect response (FEND CMD_DETECT DETECT_RESP) was received.
func probePortAtBaud(rt cliRuntime, port string, baud int) (bool, error) {
	serial, err := rt.rnodeOpenSerialAtBaud(port, baud)
	if err != nil {
		return false, err
	}
	defer func() { _ = serial.Close() }()
	if err := rnodeDetect(serial, port); err != nil {
		return false, err
	}
	data, err := readWithTimeout(serial, 700*time.Millisecond)
	if err != nil {
		return false, err
	}
	return containsDetectResponse(data), nil
}

// containsDetectResponse reports whether data contains a KISS detect-response
// frame: FEND(0xC0) CMD_DETECT(0x08) DETECT_RESP(0x46).
func containsDetectResponse(data []byte) bool {
	for i := 0; i+2 < len(data); i++ {
		if data[i] == 0xC0 && data[i+1] == 0x08 && data[i+2] == 0x46 {
			return true
		}
	}
	return false
}

// readWithTimeout reads from port, returning any data received within timeout.
// If no data arrives within timeout, it returns nil with no error. A timed-out
// goroutine is unblocked when the caller closes the port (deferred in
// probePortAtBaud).
func readWithTimeout(port serialPort, timeout time.Duration) ([]byte, error) {
	type readResult struct {
		data []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := port.Read(buf)
		ch <- readResult{buf[:n], err}
	}()
	select {
	case r := <-ch:
		return r.data, r.err
	case <-time.After(timeout):
		return nil, nil
	}
}

// listSerialPorts globs the common serial device paths across platforms and
// returns a sorted, deduplicated list. On macOS these are /dev/cu.* (and
// /dev/tty.* — but tty.* is often EBUSY, so cu.* is preferred); on Linux these
// are /dev/ttyACM* and /dev/ttyUSB*; /dev/serial/by-id/* provides stable
// per-device symlinks on both.
func listSerialPorts() []string {
	patterns := []string{
		"/dev/ttyACM*",
		"/dev/ttyUSB*",
		"/dev/cu.*",
		"/dev/serial/by-id/*",
	}
	var ports []string
	seen := make(map[string]bool)
	for _, pat := range patterns {
		matches, _ := filepath.Glob(pat)
		for _, m := range matches {
			resolved, err := filepath.EvalSymlinks(m)
			if err == nil && resolved != m {
				if !seen[resolved] {
					seen[resolved] = true
					ports = append(ports, m)
				}
			} else if !seen[m] {
				seen[m] = true
				ports = append(ports, m)
			}
		}
	}
	sort.Strings(ports)
	return ports
}
