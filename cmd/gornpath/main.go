// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornpath is a Reticulum-based path management utility.
//
// It provides features for:
//   - Viewing the current routing/path table.
//   - Requesting paths to specific destinations from the network.
//   - Managing path table entries (dropping paths).
//   - Viewing announce rate information.
//   - Managing blackhole and remote path information.
//
// Usage:
//
//	Display the path table:
//	  gornpath -t [--config <config_dir>]
//
//	Request a path to a destination:
//	  gornpath <destination_hash> [-w <timeout>] [--config <config_dir>]
//
//	Drop a path to a destination:
//	  gornpath -d <destination_hash> [--config <config_dir>]
//
//	Blackhole an identity:
//	  gornpath -B <destination_hash> [--reason <text>] [--duration <hours>]
//
// Flags:
//
//	-config string
//	      path to alternative Reticulum config directory
//	-t    show all known paths in the routing table
//	-m int
//	      maximum hops to filter path table by
//	-r    show announce rate info
//	-d    remove the path to a specified destination
//	-D    drop all queued announces
//	-w float
//	      timeout in seconds before giving up on a path request (default 15)
//	-j    output information in JSON format
//	-v    increase verbosity
//	-version
//	      show version and exit
package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/gmlewis/go-reticulum/rns"
)

type runtimeT struct {
	app    *appT
	logger *rns.Logger
}

func newRuntime(app *appT) *runtimeT {
	if app == nil {
		app = &appT{}
	}
	return &runtimeT{app: app, logger: rns.NewLogger()}
}

func main() {
	log.SetFlags(0)
	app, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		if err == errHelp {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		(&appT{}).usage(os.Stderr)
		os.Exit(2)
	}
	newRuntime(app).run()
}

func (rt *runtimeT) run() {
	if rt == nil || rt.app == nil {
		return
	}
	app := rt.app
	logger := rt.logger

	if app.version {
		fmt.Printf("gornpath %v\n", rns.VERSION)
		return
	}

	if !app.dropAnnounces && !app.table && !app.rates && len(app.args) == 0 && !app.dropVia && !app.blackholed && !app.blackhole && !app.unblackhole && !app.blackholedList {
		fmt.Println("")
		app.usage(os.Stdout)
		fmt.Println("")
		return
	}

	targetLogLevel := rns.LogNotice
	if app.verbose {
		targetLogLevel = rns.LogInfo
	}
	logger.SetLogLevel(targetLogLevel)

	ts := rns.NewTransportSystem()
	ret, err := rns.NewReticulumWithLogger(ts, app.configDir, logger)
	if err != nil {
		log.Fatalf("Could not initialize Reticulum: %v\n", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			rns.Logf("Warning: Could not close Reticulum properly: %v", rns.LogWarning, false, err)
		}
	}()

	var remoteClient remoteRequestClient
	defer func() {
		if remoteClient != nil {
			_ = remoteClient.Close()
		}
	}()

	if app.remoteHash != "" && (app.drop || app.dropVia || app.dropAnnounces || app.blackholed || app.blackhole || app.unblackhole) {
		os.Exit(255)
	}

	if app.table || app.rates {
		if app.remoteHash != "" {
			remoteHash, err := parseHash(app.remoteHash)
			if err != nil {
				log.Fatal(err)
			}
			client, err := connectRemoteClient(os.Stdout, ret.Transport(), remoteHash, app.identityPath, app.remoteTimeout, remotePurposeManagement, false)
			if err != nil {
				if errors.Is(err, errPathRequestTimedOut) {
					os.Exit(12)
				}
				log.Fatal(err)
			}
			remoteClient = client
		}
	}

	if app.table {
		if remoteClient != nil {
			var destinationHash []byte
			if len(app.args) > 0 {
				destinationHash, err = parseHash(app.args[0])
				if err != nil {
					log.Fatal(err)
				}
			}
			if err := doRemoteTable(os.Stdout, remoteClient, destinationHash, app.maxHops, app.jsonOut, app.remoteTimeout); err != nil {
				if errors.Is(err, errRemoteRequestFailed) {
					os.Exit(10)
				}
				log.Fatal(err)
			}
		} else if err := doTable(os.Stdout, ts, app.maxHops, app.jsonOut); err != nil {
			log.Fatal(err)
		}
	} else if app.rates {
		if remoteClient != nil {
			var destinationHash []byte
			if len(app.args) > 0 {
				destinationHash, err = parseHash(app.args[0])
				if err != nil {
					log.Fatal(err)
				}
			}
			if err := doRemoteRates(os.Stdout, remoteClient, destinationHash, app.jsonOut, app.remoteTimeout); err != nil {
				if errors.Is(err, errRemoteRequestFailed) {
					os.Exit(10)
				}
				log.Fatal(err)
			}
		} else if err := doRates(os.Stdout, ret, nil, app.jsonOut); err != nil {
			if errors.Is(err, errNoRateInformation) {
				os.Exit(1)
			}
			log.Fatal(err)
		}
	} else if app.blackholedList {
		if len(app.args) == 0 {
			log.Fatal("missing destination hash")
		}
		sourceHash, err := parseHash(app.args[0])
		if err != nil {
			log.Fatal(err)
		}
		client, err := connectRemoteClient(os.Stdout, ret.Transport(), sourceHash, "", app.remoteTimeout, remotePurposeBlackhole, false)
		if err != nil {
			if errors.Is(err, errPathRequestTimedOut) {
				os.Exit(12)
			}
			log.Fatal(err)
		}
		remoteClient = client
		filter := ""
		if len(app.args) > 1 {
			filter = app.args[1]
		}
		localIdentity := ret.Transport().Identity()
		var localIdentityHash []byte
		if localIdentity != nil {
			localIdentityHash = localIdentity.Hash
		}
		if err := doRemoteBlackholedList(os.Stdout, remoteClient, filter, localIdentityHash, app.jsonOut, app.remoteTimeout); err != nil {
			if errors.Is(err, errRemoteRequestFailed) {
				os.Exit(10)
			}
			log.Fatal(err)
		}
	} else if app.blackholed {
		filter := ""
		if len(app.args) > 0 {
			filter = app.args[0]
		}
		localIdentity := ret.Transport().Identity()
		var localIdentityHash []byte
		if localIdentity != nil {
			localIdentityHash = localIdentity.Hash
		}
		if err := doBlackholed(os.Stdout, ret, filter, localIdentityHash); err != nil {
			if errors.Is(err, errNoBlackholedInformation) {
				os.Exit(20)
			}
			log.Fatal(err)
		}
	} else if app.blackhole {
		if len(app.args) == 0 {
			log.Fatal("missing destination hash")
		}
		hash, err := parseHash(app.args[0])
		if err != nil {
			log.Fatal(err)
		}
		if err := doBlackhole(os.Stdout, ret, hash, app.duration, app.reason); err != nil {
			log.Fatal(err)
		}
	} else if app.unblackhole {
		if len(app.args) == 0 {
			log.Fatal("missing destination hash")
		}
		hash, err := parseHash(app.args[0])
		if err != nil {
			log.Fatal(err)
		}
		if err := doUnblackhole(os.Stdout, ret, hash); err != nil {
			log.Fatal(err)
		}
	} else if len(app.args) > 0 {
		destHex := app.args[0]
		destHash, err := hex.DecodeString(destHex)
		if err != nil {
			log.Fatalf("Invalid destination hash: %v\n", err)
		}

		if app.drop {
			if err := doDrop(os.Stdout, ts, destHash); err != nil {
				log.Fatal(err)
			}
		} else if app.dropVia {
			if err := doDropVia(os.Stdout, ts, destHash); err != nil {
				log.Fatal(err)
			}
		} else if app.dropAnnounces {
			if err := doDropAnnounces(os.Stdout, ts); err != nil {
				log.Fatal(err)
			}
		} else if err := doRequest(os.Stdout, ts, destHash, app.timeout); err != nil {
			log.Fatal(err)
		}
	}
}

type pathTableProvider interface {
	GetPathTable() []rns.PathInfo
}

func doTable(out io.Writer, ts pathTableProvider, maxHops int, jsonOut bool) error {
	rendered, err := renderPathTable(ts.GetPathTable(), maxHops, jsonOut, nil)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(out, rendered)
	return err
}
