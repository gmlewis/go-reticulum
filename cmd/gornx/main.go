// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornx provides Reticulum remote command execution compatible with rnx.
//
// It operates in two primary modes:
//   - Listen mode: expose an rnx execute endpoint and run authorized commands.
//   - Execute mode: connect to a remote rnx endpoint and run one command.
//
// Listener authorization uses hashes in:
//
//	/etc/rnx/allowed_identities
//	~/.config/rnx/allowed_identities
//	~/.rnx/allowed_identities
//
// Usage:
//
//	Listen mode:
//	  gornx -l [-i <identity_file>] [--config <config_dir>] [-v] [-q]
//
//	Execute mode:
//	  gornx <destination_hash> <command> [-i <identity_file>] [--config <config_dir>] [-v] [-q]
//
// Key flags:
//
//	-l                            Listen for incoming command requests
//	-i <identity_file>            Identity path
//	-config <config_dir>          Reticulum config directory
//	-x                            Interactive mode (placeholder)
//	-v / -q                       Logging level controls
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// AppName is the application name used for default identities and destinations.
const AppName = "rnx"

func main() {
	configDir := flag.String("config", "", "path to alternative Reticulum config directory")
	identityPath := flag.String("i", "", "path to identity to use")
	verbose := flag.Bool("v", false, "increase verbosity")
	quiet := flag.Bool("q", false, "decrease verbosity")
	listenMode := flag.Bool("l", false, "listen for incoming commands")
	interactive := flag.Bool("x", false, "enter interactive mode")
	// Add other flags as needed
	log.SetFlags(0)
	flag.Parse()

	if *verbose {
		rns.SetLogLevel(rns.LogVerbose)
	}
	if *quiet {
		rns.SetLogLevel(rns.LogWarning)
	}

	ts := rns.NewTransportSystem()
	ret, err := rns.NewReticulum(ts, *configDir)
	if err != nil {
		log.Fatalf("Could not initialize Reticulum: %v\n", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			rns.Logf("Warning: Could not close Reticulum properly: %v", rns.LogWarning, false, err)
		}
	}()

	if *listenMode {
		doListen(*identityPath)
	} else if *interactive {
		// doInteractive(...)
	} else {
		if flag.NArg() < 2 {
			flag.Usage()
			log.Fatal("destination and command must be specified")
		}
		destHashHex := flag.Arg(0)
		command := flag.Arg(1)
		doExecute(ret.Transport(), *identityPath, destHashHex, command)
	}
}

func doListen(idPath string) {
	if idPath == "" {
		home, _ := os.UserHomeDir()
		idPath = filepath.Join(home, ".reticulum", "identities", AppName)
	}

	var id *rns.Identity
	if _, err := os.Stat(idPath); err == nil {
		id, err = rns.FromFile(idPath)
		if err != nil {
			log.Fatalf("Could not load identity: %v\n", err)
		}
	} else {
		fmt.Println("Creating new identity...")
		id, _ = rns.NewIdentity(true)
		if err := id.ToFile(idPath); err != nil {
			log.Fatalf("Could not save identity %q: %v\n", idPath, err)
		}
	}

	ts := rns.NewTransportSystem()
	dest, err := rns.NewDestination(ts, id, rns.DestinationIn, rns.DestinationSingle, AppName, "execute")
	if err != nil {
		log.Fatalf("Could not create destination: %v\n", err)
	}

	// Load allowed identities
	allowedHashes := make(map[string]bool)
	home, _ := os.UserHomeDir()
	allowedFilePath := resolveAllowedIdentitiesPath(home)
	if data, err := os.ReadFile(allowedFilePath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				allowedHashes[line] = true
			}
		}
	}

	dest.RegisterRequestHandler("command", func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
		if remoteIdentity == nil {
			rns.Log("Rejected unauthenticated command request", rns.LogWarning, false)
			return nil
		}

		// Use bytes.Equal for identity check
		allowed := false
		for hash := range allowedHashes {
			if len(hash) == len(remoteIdentity.HexHash) && bytes.Equal([]byte(hash), []byte(remoteIdentity.HexHash)) {
				allowed = true
				break
			}
		}
		if !allowed {
			rns.Log(fmt.Sprintf("Rejected unauthorized command request from %v", remoteIdentity.HexHash), rns.LogWarning, false)
			return nil
		}

		// Limit input size to 64KB
		if len(data) > 64*1024 {
			rns.Log("Rejected command: input too large", rns.LogWarning, false)
			return []any{false, int64(127), []byte{}, []byte("command too large"), int64(0), int64(len("command too large")), float64(time.Now().UnixNano()) / 1e9, float64(time.Now().UnixNano()) / 1e9}
		}

		unpacked, err := rns.Unpack(data)
		if err != nil {
			rns.Log(fmt.Sprintf("Failed to unpack command: %v", err), rns.LogWarning, false)
			return []any{false, int64(127), []byte{}, []byte("invalid command encoding"), int64(0), int64(len("invalid command encoding")), float64(time.Now().UnixNano()) / 1e9, float64(time.Now().UnixNano()) / 1e9}
		}
		parts, ok := unpacked.([]any)
		if !ok || len(parts) == 0 {
			rns.Log("Malformed command parts", rns.LogWarning, false)
			return []any{false, int64(127), []byte{}, []byte("malformed command"), int64(0), int64(len("malformed command")), float64(time.Now().UnixNano()) / 1e9, float64(time.Now().UnixNano()) / 1e9}
		}
		cmdBytes, ok := parts[0].([]byte)
		if !ok {
			rns.Log("Malformed command: not []byte", rns.LogWarning, false)
			return []any{false, int64(127), []byte{}, []byte("malformed command"), int64(0), int64(len("malformed command")), float64(time.Now().UnixNano()) / 1e9, float64(time.Now().UnixNano()) / 1e9}
		}
		if len(cmdBytes) > 64*1024 {
			rns.Log("Rejected command: command string too large", rns.LogWarning, false)
			return []any{false, int64(127), []byte{}, []byte("command string too large"), int64(0), int64(len("command string too large")), float64(time.Now().UnixNano()) / 1e9, float64(time.Now().UnixNano()) / 1e9}
		}
		cmdStr := string(cmdBytes)

		rns.Log(fmt.Sprintf("Executing authorized command from %v: %v", remoteIdentity.HexHash, cmdStr), rns.LogInfo, false)

		cmd := exec.Command("sh", "-c", cmdStr)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err = cmd.Run()
		retval := 0
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				retval = exitError.ExitCode()
			} else {
				retval = -1
			}
		}

		result := []any{
			true, // executed
			int64(retval),
			stdout.Bytes(),
			stderr.Bytes(),
			int64(stdout.Len()),
			int64(stderr.Len()),
			float64(time.Now().UnixNano()) / 1e9, // started
			float64(time.Now().UnixNano()) / 1e9, // concluded
		}
		return result
	}, rns.AllowList, nil, true)

	fmt.Printf("rnx listening on %x\n", dest.Hash)

	// Keep alive
	select {}
}

func resolveAllowedIdentitiesPath(home string) string {
	candidates := []string{
		"/etc/rnx/allowed_identities",
		filepath.Join(home, ".config", "rnx", "allowed_identities"),
		filepath.Join(home, ".rnx", "allowed_identities"),
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}

	return ""
}

func doExecute(ts rns.Transport, idPath string, destHashHex string, command string) {
	if idPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Could not determine user home directory: %v\n", err)
		}
		idPath = filepath.Join(home, ".reticulum", "identities", AppName)
	}
	id, err := rns.FromFile(idPath)
	if err != nil {
		log.Fatalf("Could not load identity: %v\n", err)
	}

	destHash, err := rns.HexToBytes(destHashHex)
	if err != nil {
		log.Fatalf("Invalid destination hash %q: %v\n", destHashHex, err)
	}
	remoteID := rns.RecallIdentity(ts, destHash)
	if remoteID == nil {
		if err := ts.RequestPath(destHash); err != nil {
			log.Fatalf("Could not request path to <%x>: %v\n", destHash, err)
		}
		time.Sleep(2 * time.Second)
		remoteID = rns.RecallIdentity(ts, destHash)
	}
	if remoteID == nil {
		log.Fatalf("Could not resolve remote identity for destination %x\n", destHash)
	}

	remoteDest, err := rns.NewDestination(ts, remoteID, rns.DestinationOut, rns.DestinationSingle, AppName, "execute")
	if err != nil {
		log.Fatalf("Could not create remote destination: %v\n", err)
	}
	link, err := rns.NewLink(ts, remoteDest)
	if err != nil {
		log.Fatalf("Could not create link: %v\n", err)
	}

	established := make(chan bool, 1)
	link.SetLinkEstablishedCallback(func(l *rns.Link) {
		established <- true
	})
	if err := link.Establish(); err != nil {
		log.Fatalf("Could not establish link: %v\n", err)
	}

	select {
	case <-established:
		// Reveal identity to remote
		if err := link.Identify(id); err != nil {
			log.Fatalf("Could not identify to remote: %v\n", err)
		}
	case <-time.After(10 * time.Second):
		log.Fatalf("Link establishment timed out")
	}

	requestData := []any{
		[]byte(command),
		15.0, // timeout
		0,    // stdout limit
		0,    // stderr limit
		nil,  // stdin
	}

	fmt.Printf("Executing %v on %x...\n", command, destHash)
	_, err = link.Request("command", requestData, func(rr *rns.RequestReceipt) {
		if rr.Status == rns.RequestReady {
			res := rr.Response.([]any)
			stdout := res[2].([]byte)
			fmt.Print(string(stdout))
			os.Exit(0)
		}
	}, nil, nil, 0)

	if err != nil {
		log.Fatalf("Request failed: %v\n", err)
	}

	time.Sleep(30 * time.Second)
	log.Fatalf("Command timed out")
}
