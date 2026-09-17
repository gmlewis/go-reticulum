// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"strings"

	"github.com/gmlewis/go-reticulum/utils"
)

// errHelp reports that the command line asked for the usage text. It is a
// sentinel rather than a failure, so main can exit 0 having printed help.
var errHelp = errors.New("help requested")

// options is the command line, after parsing. Every field is an override: an
// empty string or a false boolean leaves the configuration file's value alone,
// so the command line can never accidentally erase a documented setting.
type options struct {
	// configPath overrides the appliance's configuration file. Empty means
	// GRL_HOME/config.toml, or ~/.grl/config.toml when GRL_HOME is unset.
	configPath string
	// portalAddr overrides the captive dashboard's listen address.
	portalAddr string
	// gpsPort and compassPort override the two sensor devices.
	gpsPort     string
	compassPort string
	// quiet silences the appliance's own informational lines. Errors still
	// reach standard error.
	quiet bool
	// verbose raises the Reticulum logger and prints the resolved
	// configuration, which is the way to see which file and which devices an
	// appliance actually used.
	verbose bool
	// version asks for the version string and nothing else.
	version bool
	// args holds any positional arguments, which grl does not accept.
	args []string
}

// usage writes the usage text.
func (o *options) usage(w io.Writer) {
	utils.WriteText(w, usageText)
}

// parseFlags parses the command line into options, writing usage to standard
// error when the command line asks for it.
func parseFlags(args []string) (*options, error) {
	return parseFlagsTo(args, os.Stderr)
}

// parseFlagsTo is parseFlags with the usage destination injected, so a test can
// read the usage text without capturing the process's standard error.
func parseFlagsTo(args []string, usageOutput io.Writer) (*options, error) {
	opts := &options{}
	fs := flag.NewFlagSet("grl", flag.ContinueOnError)
	// The flag package prints its own errors; this tool prints them once, in
	// main, so the duplicated wording never reaches an operator.
	fs.SetOutput(io.Discard)
	fs.Usage = func() { opts.usage(usageOutput) }

	fs.StringVar(&opts.configPath, "config", "",
		"configuration file to use (default GRL_HOME/config.toml or ~/.grl/config.toml)")
	fs.StringVar(&opts.portalAddr, "portal-addr", "",
		"captive dashboard listen address, like 127.0.0.1:9111 or 0.0.0.0:9111 (empty disables)")
	fs.StringVar(&opts.gpsPort, "gps-port", "",
		"GNSS receiver serial port, like /dev/tty.usbserial-0001 or /dev/ttyUSB0")
	fs.StringVar(&opts.compassPort, "compass-port", "",
		"electronic compass serial port, like /dev/tty.usbserial-0002 or /dev/ttyUSB1")
	fs.BoolVar(&opts.quiet, "quiet", false,
		"only report errors")
	fs.BoolVar(&opts.verbose, "verbose", false,
		"report the resolved configuration and the Reticulum log at INFO")
	fs.BoolVar(&opts.version, "version", false, "show program's version number and exit")
	fs.BoolVar(&opts.version, "V", false, "show program's version number and exit")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, err
	}
	opts.args = append([]string{}, fs.Args()...)
	if err := opts.validate(); err != nil {
		return nil, err
	}
	return opts, nil
}

// validate rejects a command line that cannot mean anything.
func (o *options) validate() error {
	if len(o.args) > 0 {
		return errors.New("unrecognized arguments: " + strings.Join(o.args, " "))
	}
	if o.quiet && o.verbose {
		return errors.New("-quiet and -verbose cannot both be set")
	}
	return nil
}

// usageText is the help text, in the shape the other tools in this repository
// use.
const usageText = `usage: grl [-h] [--version] [--config CONFIG] [--portal-addr ADDR]
           [--gps-port PORT] [--compass-port PORT] [--quiet] [--verbose]

Run the Go Reticulum Lifesaver: an off-grid survival communicator and field
assistant in a single executable

grl runs the whole appliance in one process: a local Reticulum stack, an
NMEA-0183 GNSS receiver and electronic compass (or their static fallbacks), the
shared field-assistant engine that answers with zero radio hops, and the captive
survival dashboard a smartphone can read. With no hardware attached it runs on
static test values, so the same configuration serves a desk and a trail.

options:
  -h, --help            show this help message and exit
  --version             show program's version number and exit
  --config CONFIG       configuration file to use (default GRL_HOME/config.toml
                        or ~/.grl/config.toml)
  --portal-addr ADDR    captive dashboard listen address, like 127.0.0.1:9111
                        or 0.0.0.0:9111 (empty disables the dashboard)
  --gps-port PORT       GNSS receiver serial port, like
                        /dev/tty.usbserial-0001 or /dev/ttyUSB0
  --compass-port PORT   electronic compass serial port, like
                        /dev/tty.usbserial-0002 or /dev/ttyUSB1
  --quiet               only report errors
  --verbose             report the resolved configuration and the Reticulum log
                        at INFO

files:
  CONFIG        ~/.grl/config.toml   device, dashboard, sensors, mesh rooms

The configuration file is created with documented defaults on the first run.
GRL_HOME overrides the appliance's home directory, which is how a second
appliance, or a test, stays isolated on one workstation.

safety:
  grl is experimental and NOT a certified medical or life-safety device.
  Transmission over unlicensed LoRa is never guaranteed. See DISCLAIMER.md.

Once it is running, open http://localhost:9111/ for the survival dashboard.
`
