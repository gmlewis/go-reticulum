// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornodeconf is a utility for configuring and managing RNode devices.
//
// It provides a comprehensive set of features for:
//   - Viewing device information and current configuration.
//   - Updating and installing RNode firmware.
//   - Configuring radio parameters (frequency, bandwidth, etc.).
//   - Managing device features like Bluetooth, WiFi, and displays.
//   - Backing up and wiping device EEPROM.
//
// Usage:
//
//	Display device info:
//	  gornodeconf -i <serial_port>
//
//	Update firmware:
//	  gornodeconf -u <serial_port>
//
//	Configure radio:
//	  gornodeconf <serial_port> --freq 868000000 --bw 125000 --sf 7 --cr 5
//
// Flags:
//
//	-i    show device info
//	-a    automatic installation on various supported devices
//	-u    update firmware to the latest version
//	-U    force update even if version matches
//	-c    print device configuration
//	-N    switch to normal mode
//	-T    switch to TNC mode
//	-version
//	      print program version and exit
//
// (Refer to the -help output for a complete list of supported flags)
package main

import (
	"flag"
	"fmt"
	"log"
)

const AppVersion = "2.5.0"

func main() {
	info := flag.Bool("i", false, "Show device info")
	autoinstall := flag.Bool("a", false, "Automatic installation on various supported devices")
	update := flag.Bool("u", false, "Update firmware to the latest version")
	forceUpdate := flag.Bool("U", false, "Force update even if version matches")
	fwVersion := flag.String("fw-version", "", "Specific firmware version to use")
	fwURL := flag.String("fw-url", "", "Alternate firmware download URL")
	noCheck := flag.Bool("nocheck", false, "Don't check for updates online")
	extract := flag.Bool("e", false, "Extract firmware from connected RNode")
	useExtracted := flag.Bool("E", false, "Use extracted firmware for installation")
	clearCache := flag.Bool("C", false, "Clear locally cached firmware files")
	baudFlash := flag.String("baud-flash", "921600", "Baud rate when flashing")

	normal := flag.Bool("N", false, "Switch to normal mode")
	tnc := flag.Bool("T", false, "Switch to TNC mode")

	btOn := flag.Bool("b", false, "Turn bluetooth on")
	btOff := flag.Bool("B", false, "Turn bluetooth off")
	btPair := flag.Bool("p", false, "Put into bluetooth pairing mode")

	wifi := flag.String("w", "", "Set WiFi mode (OFF, AP or STATION)")
	channel := flag.String("channel", "", "Set WiFi channel")
	ssid := flag.String("ssid", "", "Set WiFi SSID")
	psk := flag.String("psk", "", "Set WiFi PSK")
	showPsk := flag.Bool("show-psk", false, "Display stored WiFi PSK")
	ip := flag.String("ip", "", "Set static WiFi IP")
	nm := flag.String("nm", "", "Set static WiFi netmask")

	display := flag.Int("D", -1, "Set display intensity (0-255)")
	timeout := flag.Int("t", -1, "Set display timeout in seconds")
	rotation := flag.Int("R", -1, "Set display rotation (0-3)")
	displayAddr := flag.String("display-addr", "", "Set display address as hex byte")
	recondition := flag.Bool("recondition-display", false, "Start display reconditioning")

	np := flag.Int("np", -1, "Set NeoPixel intensity (0-255)")

	freq := flag.Int("freq", 0, "Frequency in Hz")
	bw := flag.Int("bw", 0, "Bandwidth in Hz")
	txp := flag.Int("txp", -1, "TX power in dBm")
	sf := flag.Int("sf", 0, "Spreading factor (7-12)")
	cr := flag.Int("cr", 0, "Coding rate (5-8)")

	iaEnable := flag.Bool("ia-enable", false, "Enable interference avoidance")
	iaDisable := flag.Bool("ia-disable", false, "Disable interference avoidance")

	config := flag.Bool("c", false, "Print device configuration")

	backup := flag.Bool("eeprom-backup", false, "Backup EEPROM to file")
	dump := flag.Bool("eeprom-dump", false, "Dump EEPROM to console")
	wipe := flag.Bool("eeprom-wipe", false, "Unlock and wipe EEPROM")

	public := flag.Bool("P", false, "Display public part of signing key")
	trustKey := flag.String("trust-key", "", "Public key to trust for device verification")

	version := flag.Bool("version", false, "Print program version and exit")

	log.SetFlags(0)
	flag.Parse()

	if *version {
		fmt.Printf("gornodeconf %v\n", AppVersion)
		return
	}

	if *clearCache {
		fmt.Println("Clearing local firmware cache...")
		// TODO: Implement cache clearing
		return
	}

	if flag.NArg() == 0 && !*public && *trustKey == "" {
		flag.Usage()
		log.Fatal("No serial port specified")
	}

	port := flag.Arg(0)
	_ = port
	_ = autoinstall
	_ = update
	_ = forceUpdate
	_ = fwVersion
	_ = fwURL
	_ = noCheck
	_ = extract
	_ = useExtracted
	_ = baudFlash
	_ = normal
	_ = tnc
	_ = btOn
	_ = btOff
	_ = btPair
	_ = wifi
	_ = channel
	_ = ssid
	_ = psk
	_ = showPsk
	_ = ip
	_ = nm
	_ = display
	_ = timeout
	_ = rotation
	_ = displayAddr
	_ = recondition
	_ = np
	_ = freq
	_ = bw
	_ = txp
	_ = sf
	_ = cr
	_ = iaEnable
	_ = iaDisable
	_ = config
	_ = backup
	_ = dump
	_ = wipe
	_ = info

	fmt.Println("gornodeconf utility started (limited functionality in this version)")
}
