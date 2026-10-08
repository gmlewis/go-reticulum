// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file parses the gonsensor command-line flags. There is no Python
// original to mirror: gonsensor exists so that the sensor front end of an
// Android appliance can be written in the same language, with the same tests,
// as the sensor parsers it feeds.

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
)

// errHelp is the help sentinel. main maps it onto a clean exit, because asking
// for help is not an error.
var errHelp = errors.New("help requested")

// Defaults for the command line. They are the rates the bot's own receivers
// comfortably keep up with: a GNSS receiver fixes at about 1 Hz, and a
// magnetometer is polled far faster than any heading display can follow.
const (
	// defaultRate is the location sentence rate in hertz.
	defaultRate = 1
	// defaultCompassRate is the heading sentence rate in hertz.
	defaultCompassRate = 10
	// defaultStatusInterval is the seconds between status lines on stderr.
	defaultStatusInterval = 5
)

// options is the command line after parsing.
type options struct {
	// rate is the maximum number of location sentences to emit per second. A
	// zero rate disables the limit.
	rate float64
	// compassRate is the maximum number of heading sentences to emit per
	// second. A zero rate disables the limit.
	compassRate float64
	// noRMC suppresses the recommended-minimum position sentence.
	noRMC bool
	// noGGA suppresses the fix-quality sentence.
	noGGA bool
	// noHDM suppresses the magnetic-heading sentence.
	noHDM bool
	// noHDT suppresses the true-heading sentence.
	noHDT bool
	// noGST suppresses the position-error sentence.
	noGST bool
	// statusInterval is the seconds between summary lines on the status
	// stream. A zero interval disables the summary.
	statusInterval float64
	// version asks for the version string and nothing else.
	version bool
}

// parseFlags parses the gonsensor argument list. The usage text goes to
// usageOutput, so a caller can capture it instead of the process's stderr.
func parseFlags(args []string, usageOutput io.Writer) (*options, error) {
	opts := &options{
		rate:           defaultRate,
		compassRate:    defaultCompassRate,
		statusInterval: defaultStatusInterval,
	}
	fs := flag.NewFlagSet("gonsensor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { printUsage(usageOutput) }

	fs.Func("rate", "Maximum location sentences per second (0 disables the limit)", func(v string) error {
		f, err := parseNonNegative("rate", v)
		if err != nil {
			return err
		}
		opts.rate = f
		return nil
	})
	fs.Func("compass-rate", "Maximum heading sentences per second (0 disables the limit)", func(v string) error {
		f, err := parseNonNegative("compass-rate", v)
		if err != nil {
			return err
		}
		opts.compassRate = f
		return nil
	})
	fs.BoolVar(&opts.noRMC, "no-rmc", false, "Do not emit the recommended-minimum position sentence")
	fs.BoolVar(&opts.noGGA, "no-gga", false, "Do not emit the fix-quality sentence")
	fs.BoolVar(&opts.noHDM, "no-hdm", false, "Do not emit the magnetic-heading sentence")
	fs.BoolVar(&opts.noHDT, "no-hdt", false, "Do not emit the true-heading sentence")
	fs.BoolVar(&opts.noGST, "no-gst", false, "Do not emit the position-error sentence")
	fs.Func("status-interval", "Seconds between status lines on stderr (0 disables)", func(v string) error {
		f, err := parseNonNegative("status-interval", v)
		if err != nil {
			return err
		}
		opts.statusInterval = f
		return nil
	})
	fs.BoolVar(&opts.version, "version", false, "show program's version number and exit")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, err
	}
	if remaining := fs.Args(); len(remaining) > 0 {
		return nil, errors.New("unrecognized arguments: " + remaining[0])
	}
	return opts, nil
}

// parseNonNegative reads a flag value that must be a finite number of zero or
// more. A negative rate is refused rather than treated as "unlimited", because
// a silently inverted limit is a stream at the wrong rate.
func parseNonNegative(name, v string) (float64, error) {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("invalid value %q for --%v: not a number", v, name)
	}
	if f < 0 {
		return 0, fmt.Errorf("invalid value %q for --%v: must not be negative", v, name)
	}
	return f, nil
}

// printUsage writes the usage text to w.
func printUsage(w io.Writer) {
	if w == nil {
		w = io.Discard
	}
	_, _ = fmt.Fprint(w, usageText)
}

// usageText is the help output.
const usageText = `usage: gonsensor [-h] [--rate RATE] [--compass-rate COMPASS_RATE]
                 [--no-rmc] [--no-gga] [--no-hdm] [--no-hdt]
                 [--status-interval STATUS_INTERVAL] [--version]

Convert a JSON sensor sample stream on stdin into NMEA-0183 sentences on stdout

options:
  -h, --help            show this help message and exit
  --rate RATE           Maximum location sentences per second (0 disables the
                        limit; default 1)
  --compass-rate COMPASS_RATE
                        Maximum heading sentences per second (0 disables the
                        limit; default 10)
  --no-rmc              Do not emit the recommended-minimum position sentence
  --no-gga              Do not emit the fix-quality sentence
  --no-hdm              Do not emit the magnetic-heading sentence
  --no-hdt              Do not emit the true-heading sentence
  --no-gst              Do not emit the position-error sentence
  --status-interval STATUS_INTERVAL
                        Seconds between status lines on stderr (0 disables;
                        default 5)
  --version             show program's version number and exit
`
