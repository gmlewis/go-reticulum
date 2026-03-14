// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornsd is the Reticulum Network Stack daemon.
//
// It is the core background process that:
//   - Manages network interfaces and peer connections.
//   - Handles packet routing and transport.
//   - Serves path requests and manages the routing table.
//   - Provides a shared instance for other local RNS applications.
//
// Usage:
//
//	Run the daemon:
//	  gornsd [-v] [-q] [-s] [--config <config_dir>]
//
//	Print example configuration:
//	  gornsd --exampleconfig
//
// Flags:
//
//	-config string
//	      path to alternative Reticulum config directory
//	-v    increase verbosity
//	-q    decrease verbosity
//	-s    run as a service and log to file instead of stdout
//	-exampleconfig
//	      print verbose configuration example to stdout and exit
//	-version
//	      show version and exit
package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/gmlewis/go-reticulum/rns"
)

func main() {
	configDir := flag.String("config", "", "path to alternative Reticulum config directory")
	verbose := flag.Bool("v", false, "increase verbosity")
	quiet := flag.Bool("q", false, "decrease verbosity")
	service := flag.Bool("s", false, "rnsd is running as a service and should log to file")
	exampleConfig := flag.Bool("exampleconfig", false, "print verbose configuration example to stdout and exit")
	version := flag.Bool("version", false, "show version and exit")
	flag.Parse()

	if *version {
		fmt.Printf("gornsd %v\n", rns.Version)
		return
	}

	if *exampleConfig {
		fmt.Print(exampleRNSConfig)
		return
	}

	if *verbose {
		rns.SetLogLevel(rns.LogVerbose)
	}
	if *quiet {
		rns.SetLogLevel(rns.LogWarning)
	}
	if *service {
		rns.LogDest = rns.LogDestFile
	}

	_, err := rns.NewReticulum(*configDir)
	if err != nil {
		log.Fatalf("Could not initialize Reticulum: %v\n", err)
	}

	rns.Log(fmt.Sprintf("Started gornsd version %v", rns.Version), rns.LogNotice, false)

	// Keep alive
	select {}
}

const exampleRNSConfig = `# This is an example Reticulum config file.
# You should probably edit it to include any additional,
# interfaces and settings you might need.

[reticulum]

# If you enable Transport, your system will route traffic
# for other peers, pass announces and serve path requests.
# This should be done for systems that are suited to act
# as transport nodes, ie. if they are stationary and
# always-on. This directive is optional and can be removed
# for brevity.

enable_transport = No


# By default, the first program to launch the Reticulum
# Network Stack will create a shared instance, that other
# programs can communicate with. Only the shared instance
# opens all the configured interfaces directly, and other
# local programs communicate with the shared instance over
# a local socket. This is completely transparent to the
# user, and should generally be turned on. This directive
# is optional and can be removed for brevity.

share_instance = Yes

[logging]
loglevel = 4

[interfaces]

  [[Default Interface]]
    type = AutoInterface
    enabled = yes
`
