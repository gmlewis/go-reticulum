// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"flag"
	"io"

	"github.com/gmlewis/go-reticulum/utils"
)

var errHelp = errors.New("help requested")

type options struct {
	dryRun  bool
	verbose bool
	target  string
	version bool
	args    []string
}

const usageText = `usage: update-offline-data [-h] [--version] [-n] [-v] [--target TARGET]

Refresh the embedded public datasets used in go-reticulum

update-offline-data fetches the authoritative public catalogs (NOAA National
Data Buoy Center, NOAA CO-OPS Tide Predictions, OurAirports Aviation
Facilities, and the NOAA World Magnetic Model notice), filters them into
compact reference tables, and rewrites the generated files in the bot package.

The embedded tables are the providers' catalogs IN FULL: every station that
meets the table's stated filter is carried, so the field tools can answer
"nearest" anywhere on earth and let search and pagination decide what fits on a
page. Adding or dropping a station is a filter change here, never a hand edit to
a generated file.

A run is idempotent: it rewrites a table only when upstream differs from the
file, and the next run reports the file as up to date. A fetch is retried with
exponential backoff when it fails transiently, and a download that did not
arrive whole is refused rather than written.

options:
  -h, --help            show this help message and exit
  --version             show program's version number and exit
  -n, --dry-run         check upstream and report what would change without
                        writing to disk
  -v, --verbose         print the fetch, the source fingerprint (bytes and
                        sha256), and the per-field drift of each table
  --target TARGET       limit to a specific dataset: all (default), buoy, tide,
                        metar, wmm

datasets:
  buoy     NOAA NDBC stations that report weather (bot/buoy-stations.go)
  tide     NOAA CO-OPS tide-prediction stations (bot/tide-stations.go)
  metar    OurAirports fields with an IATA code and scheduled service
           (bot/metar-stations.go)
  wmm      NOAA NCEI World Magnetic Model notice (bot/declination.go)
`

func parseFlags(args []string, usageOutput io.Writer) (*options, error) {
	fs := flag.NewFlagSet("update-offline-data", flag.ContinueOnError)
	fs.SetOutput(usageOutput)
	fs.Usage = func() {
		utils.WriteText(usageOutput, usageText)
	}

	opts := &options{}
	fs.BoolVar(&opts.dryRun, "n", false, "dry run")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "dry run")
	fs.BoolVar(&opts.verbose, "v", false, "verbose output")
	fs.BoolVar(&opts.verbose, "verbose", false, "verbose output")
	fs.StringVar(&opts.target, "target", "all", "dataset target")
	fs.BoolVar(&opts.version, "version", false, "show version")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, err
	}

	opts.args = fs.Args()
	return opts, nil
}
