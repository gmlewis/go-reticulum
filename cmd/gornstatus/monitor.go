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
func runMonitor(r *rns.Reticulum, nameFilter string, verbosity int, app *appT) {
	for {
		var buf bytes.Buffer
		programSetup(programSetupParams{
			configDir:          app.configDir,
			dispAll:            app.showAll,
			verbosity:          verbosity,
			nameFilter:         nameFilter,
			jsonOutput:         app.jsonOutput,
			announceStats:      app.announceStats,
			linkStats:          app.linkStats,
			sorting:            app.sortKey,
			sortReverse:        app.sortReverse,
			remote:             app.remoteHash,
			managementIdentity: app.identityPath,
			remoteTimeout:      app.remoteTimeout,
			mustExit:           false,
			rnsInstance:        r,
			trafficTotals:      app.trafficTotals,
			discoveredIfaces:   app.discovered,
			configEntries:      app.detailedDiscovered,
			writer:             &buf,
		})

		_, _ = fmt.Fprint(os.Stdout, "\033[H\033[2J")
		_, _ = fmt.Fprint(os.Stdout, buf.String())

		time.Sleep(time.Duration(app.monitorInterval * float64(time.Second)))
	}
}
