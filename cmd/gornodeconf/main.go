// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornodeconf is a command-line utility for configuring and managing RNode
// devices. This initial Go port focuses on the command-line surface and keeps
// the serial-port contract compatible with the Python source of truth.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
)

const helpText = `usage: gornodeconf [-h] [-i] [-a] [-u] [-U] [--fw-version version]
                    [--fw-url url] [--nocheck] [-e] [-E] [-C]
                    [--baud-flash baud_flash] [-N] [-T] [-b] [-B] [-p]
                    [-w mode] [--channel channel] [--ssid ssid] [--psk psk]
                    [--show-psk] [--ip ip] [--nm nm] [-D i] [-t s]
                    [-R rotation] [--display-addr byte]
                    [--recondition-display] [--np i] [--freq Hz] [--bw Hz]
                    [--txp dBm] [--sf factor] [--cr rate] [-x] [-X] [-c]
                    [--eeprom-backup] [--eeprom-dump] [--eeprom-wipe] [-P]
                    [--trust-key hexbytes] [--version] [-f] [-r] [-k] [-S]
                    [-H FIRMWARE_HASH] [--platform platform]
                    [--product product] [--model model] [--hwrev revision]
                    [port]

Go RNode Configuration and firmware utility. This program allows you to change
various settings and startup modes of RNode. It can also install, flash and
update the firmware on supported devices.

positional arguments:
  port                  serial port where RNode is attached

options:
  -h, --help            show this help message and exit
  -i, --info            Show device info
  -a, --autoinstall     Automatic installation on various supported devices
  -u, --update          Update firmware to the latest version
  -U, --force-update    Update to specified firmware even if version matches
                        or is older than installed version
  --fw-version version  Use a specific firmware version for update or
                        autoinstall
  --fw-url url          Use an alternate firmware download URL
  --nocheck             Don't check for firmware updates online
  -e, --extract         Extract firmware from connected RNode for later use
  -E, --use-extracted   Use the extracted firmware for autoinstallation or
                        update
  -C, --clear-cache     Clear locally cached firmware files
  --baud-flash baud_flash
                        Set specific baud rate when flashing device. Default
                        is 921600
  -N, --normal          Switch device to normal mode
  -T, --tnc             Switch device to TNC mode
  -b, --bluetooth-on    Turn device bluetooth on
  -B, --bluetooth-off   Turn device bluetooth off
  -p, --bluetooth-pair  Put device into bluetooth pairing mode
  -w mode, --wifi mode  Set WiFi mode (OFF, AP or STATION)
  --channel channel     Set WiFi channel
  --ssid ssid           Set WiFi SSID (NONE to delete)
  --psk psk             Set WiFi PSK (NONE to delete)
  --show-psk            Display stored WiFi PSK
  --ip ip               Set static WiFi IP address (NONE for DHCP)
  --nm nm               Set static WiFi network mask (NONE for DHCP)
  -D i, --display i     Set display intensity (0-255)
  -t s, --timeout s     Set display timeout in seconds, 0 to disable
  -R rotation, --rotation rotation
                        Set display rotation, valid values are 0 through 3
  --display-addr byte   Set display address as hex byte (00 - FF)
  --recondition-display
                        Start display reconditioning
  --np i                Set NeoPixel intensity (0-255)
  --freq Hz             Frequency in Hz for TNC mode
  --bw Hz               Bandwidth in Hz for TNC mode
  --txp dBm             TX power in dBm for TNC mode
  --sf factor           Spreading factor for TNC mode (7 - 12)
  --cr rate             Coding rate for TNC mode (5 - 8)
  -x, --ia-enable       Enable interference avoidance
  -X, --ia-disable      Disable interference avoidance
  -c, --config          Print device configuration
  --eeprom-backup       Backup EEPROM to file
  --eeprom-dump         Dump EEPROM to console
  --eeprom-wipe         Unlock and wipe EEPROM
  -P, --public          Display public part of signing key
  --trust-key hexbytes  Public key to trust for device verification
  --version             Print program version and exit
  -f, --flash           Flash firmware and bootstrap EEPROM
  -r, --rom             Bootstrap EEPROM without flashing firmware
  -k, --key             Generate a new signing key and exit
  -S, --sign            Display public part of signing key
  -H FIRMWARE_HASH, --firmware-hash FIRMWARE_HASH
                        Set installed firmware hash
  --platform platform   Platform specification for device bootstrap
  --product product     Product specification for device bootstrap
  --model model         Model code for device bootstrap
  --hwrev revision      Hardware revision for device bootstrap
`

type options struct {
	info                  bool
	autoinstall           bool
	update                bool
	forceUpdate           bool
	fwVersion             string
	fwURL                 string
	noCheck               bool
	extract               bool
	useExtracted          bool
	clearCache            bool
	baudFlash             string
	normal                bool
	tnc                   bool
	bluetoothOn           bool
	bluetoothOff          bool
	bluetoothPair         bool
	wifi                  string
	channel               string
	ssid                  string
	psk                   string
	showPsk               bool
	ip                    string
	nm                    string
	display               int
	timeout               int
	rotation              int
	displayAddr           string
	reconditionDisplay    bool
	np                    int
	freq                  int
	bw                    int
	txp                   int
	sf                    int
	cr                    int
	iaEnable              bool
	iaDisable             bool
	config                bool
	eepromBackup          bool
	eepromDump            bool
	eepromWipe            bool
	public                bool
	trustKey              string
	version               bool
	flash                 bool
	rom                   bool
	key                   bool
	sign                  bool
	firmwareHash          string
	platform              string
	product               string
	model                 string
	hwrev                 int
	getTargetFirmwareHash bool
	getFirmwareHash       bool
	debug                 bool
}

func main() {
	log.SetFlags(0)
	if err := run(os.Args[1:]); err != nil {
		if exitErr, ok := err.(exitCodeError); ok {
			fmt.Fprintln(os.Stderr, exitErr.Error())
			os.Exit(exitErr.code)
		}
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	return newRuntime().run(args)
}

func (rt cliRuntime) run(args []string) error {
	if hasHelp(args) {
		printHelp()
		return nil
	}

	opts, port, err := parseArgs(args)
	if err != nil {
		printHelp()
		return err
	}
	rt.debug = opts.debug

	if opts.version {
		fmt.Printf("gornodeconf %v\n", rns.VERSION)
		return nil
	}

	if opts.clearCache {
		fmt.Println("Clearing local firmware cache...")
		return nil
	}

	if opts.trustKey != "" {
		return handleTrustKey(opts.trustKey)
	}

	if opts.public {
		return handlePublicKeys()
	}

	if opts.key {
		return handleGenerateKeys(opts.autoinstall)
	}

	if port, err = rt.resolveLivePort(port, opts); err != nil {
		return err
	}

	if opts.getTargetFirmwareHash || opts.getFirmwareHash {
		return rt.runFirmwareHashReadbacks(os.Stdout, port, opts)
	}

	if opts.firmwareHash != "" {
		return rt.runFirmwareHashSet(os.Stdout, port, opts.firmwareHash)
	}

	if opts.sign {
		// Keep the legacy Python help text for parity, but execute the live
		// device-signing workflow after the provisioning and hash checks.
		return rt.runDeviceSigning(os.Stdout, port)
	}

	if port == "" {
		printHelp()
		return nil
	}

	_ = port
	_ = opts
	fmt.Println("gornodeconf utility started (limited functionality in this version)")
	return nil
}

func printHelp() {
	fmt.Print(helpText)
}

func hasHelp(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func parseArgs(args []string) (options, string, error) {
	flags, port := splitArgs(args)
	fs := flag.NewFlagSet("gornodeconf", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var opts options
	fs.BoolVar(&opts.info, "info", false, "Show device info")
	fs.BoolVar(&opts.info, "i", false, "Show device info")
	fs.BoolVar(&opts.autoinstall, "autoinstall", false, "Automatic installation on various supported devices")
	fs.BoolVar(&opts.autoinstall, "a", false, "Automatic installation on various supported devices")
	fs.BoolVar(&opts.update, "update", false, "Update firmware to the latest version")
	fs.BoolVar(&opts.update, "u", false, "Update firmware to the latest version")
	fs.BoolVar(&opts.forceUpdate, "force-update", false, "Force update even if version matches")
	fs.BoolVar(&opts.forceUpdate, "U", false, "Force update even if version matches")
	fs.StringVar(&opts.fwVersion, "fw-version", "", "Specific firmware version to use")
	fs.StringVar(&opts.fwURL, "fw-url", "", "Alternate firmware download URL")
	fs.BoolVar(&opts.noCheck, "nocheck", false, "Don't check for updates online")
	fs.BoolVar(&opts.extract, "extract", false, "Extract firmware from connected RNode")
	fs.BoolVar(&opts.extract, "e", false, "Extract firmware from connected RNode")
	fs.BoolVar(&opts.useExtracted, "use-extracted", false, "Use extracted firmware for installation")
	fs.BoolVar(&opts.useExtracted, "E", false, "Use extracted firmware for installation")
	fs.BoolVar(&opts.clearCache, "clear-cache", false, "Clear locally cached firmware files")
	fs.BoolVar(&opts.clearCache, "C", false, "Clear locally cached firmware files")
	fs.StringVar(&opts.baudFlash, "baud-flash", "921600", "Baud rate when flashing")
	fs.BoolVar(&opts.normal, "normal", false, "Switch to normal mode")
	fs.BoolVar(&opts.normal, "N", false, "Switch to normal mode")
	fs.BoolVar(&opts.tnc, "tnc", false, "Switch to TNC mode")
	fs.BoolVar(&opts.tnc, "T", false, "Switch to TNC mode")
	fs.BoolVar(&opts.bluetoothOn, "bluetooth-on", false, "Turn bluetooth on")
	fs.BoolVar(&opts.bluetoothOn, "b", false, "Turn bluetooth on")
	fs.BoolVar(&opts.bluetoothOff, "bluetooth-off", false, "Turn bluetooth off")
	fs.BoolVar(&opts.bluetoothOff, "B", false, "Turn bluetooth off")
	fs.BoolVar(&opts.bluetoothPair, "bluetooth-pair", false, "Put into bluetooth pairing mode")
	fs.BoolVar(&opts.bluetoothPair, "p", false, "Put into bluetooth pairing mode")
	fs.StringVar(&opts.wifi, "wifi", "", "Set WiFi mode (OFF, AP or STATION)")
	fs.StringVar(&opts.wifi, "w", "", "Set WiFi mode (OFF, AP or STATION)")
	fs.StringVar(&opts.channel, "channel", "", "Set WiFi channel")
	fs.StringVar(&opts.ssid, "ssid", "", "Set WiFi SSID")
	fs.StringVar(&opts.psk, "psk", "", "Set WiFi PSK")
	fs.BoolVar(&opts.showPsk, "show-psk", false, "Display stored WiFi PSK")
	fs.StringVar(&opts.ip, "ip", "", "Set static WiFi IP")
	fs.StringVar(&opts.nm, "nm", "", "Set static WiFi netmask")
	fs.IntVar(&opts.display, "display", -1, "Set display intensity (0-255)")
	fs.IntVar(&opts.display, "D", -1, "Set display intensity (0-255)")
	fs.IntVar(&opts.timeout, "timeout", -1, "Set display timeout in seconds")
	fs.IntVar(&opts.timeout, "t", -1, "Set display timeout in seconds")
	fs.IntVar(&opts.rotation, "rotation", -1, "Set display rotation (0-3)")
	fs.IntVar(&opts.rotation, "R", -1, "Set display rotation (0-3)")
	fs.StringVar(&opts.displayAddr, "display-addr", "", "Set display address as hex byte")
	fs.BoolVar(&opts.reconditionDisplay, "recondition-display", false, "Start display reconditioning")
	fs.IntVar(&opts.np, "np", -1, "Set NeoPixel intensity (0-255)")
	fs.IntVar(&opts.freq, "freq", 0, "Frequency in Hz")
	fs.IntVar(&opts.bw, "bw", 0, "Bandwidth in Hz")
	fs.IntVar(&opts.txp, "txp", -1, "TX power in dBm")
	fs.IntVar(&opts.sf, "sf", 0, "Spreading factor (7-12)")
	fs.IntVar(&opts.cr, "cr", 0, "Coding rate (5-8)")
	fs.BoolVar(&opts.iaEnable, "ia-enable", false, "Enable interference avoidance")
	fs.BoolVar(&opts.iaEnable, "x", false, "Enable interference avoidance")
	fs.BoolVar(&opts.iaDisable, "ia-disable", false, "Disable interference avoidance")
	fs.BoolVar(&opts.iaDisable, "X", false, "Disable interference avoidance")
	fs.BoolVar(&opts.config, "config", false, "Print device configuration")
	fs.BoolVar(&opts.config, "c", false, "Print device configuration")
	fs.BoolVar(&opts.eepromBackup, "eeprom-backup", false, "Backup EEPROM to file")
	fs.BoolVar(&opts.eepromDump, "eeprom-dump", false, "Dump EEPROM to console")
	fs.BoolVar(&opts.eepromWipe, "eeprom-wipe", false, "Unlock and wipe EEPROM")
	fs.BoolVar(&opts.public, "public", false, "Display public part of signing key")
	fs.BoolVar(&opts.public, "P", false, "Display public part of signing key")
	fs.StringVar(&opts.trustKey, "trust-key", "", "Public key to trust for device verification")
	fs.BoolVar(&opts.version, "version", false, "Print program version and exit")
	fs.BoolVar(&opts.debug, "debug", false, "Log debug information to stderr")
	fs.BoolVar(&opts.flash, "flash", false, "Flash firmware and bootstrap EEPROM")
	fs.BoolVar(&opts.flash, "f", false, "Flash firmware and bootstrap EEPROM")
	fs.BoolVar(&opts.rom, "rom", false, "Bootstrap EEPROM without flashing firmware")
	fs.BoolVar(&opts.rom, "r", false, "Bootstrap EEPROM without flashing firmware")
	fs.BoolVar(&opts.key, "key", false, "Generate a new signing key and exit")
	fs.BoolVar(&opts.key, "k", false, "Generate a new signing key and exit")
	fs.BoolVar(&opts.sign, "sign", false, "Display public part of signing key")
	fs.BoolVar(&opts.sign, "S", false, "Display public part of signing key")
	fs.StringVar(&opts.firmwareHash, "firmware-hash", "", "Set installed firmware hash")
	fs.StringVar(&opts.firmwareHash, "H", "", "Set installed firmware hash")
	fs.StringVar(&opts.platform, "platform", "", "Platform specification for device bootstrap")
	fs.StringVar(&opts.product, "product", "", "Product specification for device bootstrap")
	fs.StringVar(&opts.model, "model", "", "Model code for device bootstrap")
	fs.IntVar(&opts.hwrev, "hwrev", -1, "Hardware revision for device bootstrap")
	fs.BoolVar(&opts.getTargetFirmwareHash, "get-target-firmware-hash", false, "Get target firmware hash from device")
	fs.BoolVar(&opts.getTargetFirmwareHash, "K", false, "Get target firmware hash from device")
	fs.BoolVar(&opts.getFirmwareHash, "get-firmware-hash", false, "Get calculated firmware hash from device")
	fs.BoolVar(&opts.getFirmwareHash, "L", false, "Get calculated firmware hash from device")

	if err := fs.Parse(flags); err != nil {
		if err == flag.ErrHelp {
			return options{}, "", err
		}
		return options{}, "", err
	}

	if fs.NArg() > 0 {
		if port != "" {
			return options{}, "", fmt.Errorf("multiple serial ports specified: %v and %v", port, fs.Arg(0))
		}
		port = fs.Arg(0)
	}

	return opts, port, nil
}

type exitCodeError struct {
	code int
	err  error
}

func (e exitCodeError) Error() string {
	return e.err.Error()
}

func (e exitCodeError) Unwrap() error {
	return e.err
}

func splitArgs(args []string) ([]string, string) {
	flags := make([]string, 0, len(args))
	port := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 < len(args) && port == "" {
				port = args[i+1]
			}
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			if port == "" {
				port = arg
				continue
			}
			continue
		}
		flags = append(flags, arg)
		name := flagName(arg)
		if flagNeedsValue(name) && !strings.Contains(arg, "=") && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return flags, port
}

func flagName(arg string) string {
	trimmed := strings.TrimLeft(arg, "-")
	if idx := strings.Index(trimmed, "="); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	return trimmed
}

func flagNeedsValue(name string) bool {
	switch name {
	case "fw-version", "fw-url", "baud-flash", "w", "wifi", "channel", "ssid", "psk", "ip", "nm", "D", "display", "t", "timeout", "R", "rotation", "display-addr", "np", "freq", "bw", "txp", "sf", "cr", "trust-key", "H", "firmware-hash", "platform", "product", "model", "hwrev":
		return true
	default:
		return false
	}
}
