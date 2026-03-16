// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// runMonitor implements the -m/--monitor mode. It clears the screen,
// captures programSetup output into a buffer, prints it, then sleeps
// for monitorInterval seconds and repeats.
func runMonitor(r *rns.Reticulum, nameFilter string, verbosity int) {
	for {
		var buf bytes.Buffer
		programSetup(programSetupParams{
			configDir:          configDir,
			dispAll:            showAll,
			verbosity:          verbosity,
			nameFilter:         nameFilter,
			jsonOutput:         jsonOutput,
			announceStats:      announceStats,
			linkStats:          linkStats,
			sorting:            sortKey,
			sortReverse:        sortReverse,
			remote:             remoteHash,
			managementIdentity: identityPath,
			remoteTimeout:      remoteTimeout,
			mustExit:           false,
			rnsInstance:        r,
			trafficTotals:      trafficTotals,
			discoveredIfaces:   discovered,
			configEntries:      detailedDiscovered,
			writer:             &buf,
		})

		fmt.Fprint(os.Stdout, "\033[H\033[2J")
		fmt.Fprint(os.Stdout, buf.String())

		time.Sleep(time.Duration(monitorInterval * float64(time.Second)))
	}
}
