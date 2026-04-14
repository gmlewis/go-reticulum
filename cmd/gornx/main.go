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
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/utils"
)

// AppName is the application name used for default identities and destinations.
const AppName = "rnx"

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
		if err == errVersion {
			fmt.Printf("gornx %v\n", rns.VERSION)
			return
		}
		log.Fatal(err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("")
		os.Exit(0)
	}()

	newRuntime(app).run()
}

func (rt *runtimeT) run() {
	if rt == nil || rt.app == nil {
		return
	}
	app := rt.app
	logger := rt.logger

	if !app.listenMode && !app.printIdentity && !app.interactive && len(app.args) == 0 {
		fmt.Println("")
		app.usage(os.Stdout)
		fmt.Println("")
		return
	}

	logLevel := rns.LogNotice
	if app.verbosity > 0 {
		if app.verbosity > 1 {
			logLevel = rns.LogDebug
		} else {
			logLevel = rns.LogVerbose
		}
	}
	if app.quietness > 0 {
		if app.quietness > 1 {
			logLevel = rns.LogError
		} else {
			logLevel = rns.LogWarning
		}
	}
	logger.SetLogLevel(logLevel)

	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, app.configDir, logger)
	if err != nil {
		log.Fatalf("Could not initialize Reticulum: %v\n", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			logger.Warning("Could not close Reticulum properly: %v", err)
		}
	}()

	// Give interfaces a moment to start
	time.Sleep(2 * time.Second)

	if app.listenMode || app.printIdentity {
		rt.doListen(ts)
	} else if len(app.args) >= 2 {
		destHashHex := app.args[0]
		command := app.args[1]
		rt.doExecute(ts, destHashHex, command)
	}

	if app.interactive && len(app.args) >= 1 {
		rt.doInteractive(ts, app.args[0])
	} else if !app.listenMode && !app.printIdentity {
		fmt.Println("")
		app.usage(os.Stdout)
		fmt.Println("")
	}
}

func (rt *runtimeT) prepareIdentity(idPath string) *rns.Identity {
	app := rt.app
	logger := rt.logger

	if idPath == "" {
		idPath = filepath.Join(app.configDir, "identities", AppName)
	}

	var id *rns.Identity
	if _, err := os.Stat(idPath); err == nil {
		id, err = rns.FromFile(idPath, logger)
		if err != nil {
			log.Fatalf("Could not load identity: %v\n", err)
		}
	} else {
		logger.Info("No valid saved identity found, creating new...")
		id, _ = rns.NewIdentity(true, logger)
		if err := os.MkdirAll(filepath.Dir(idPath), 0o700); err != nil {
			log.Fatalf("Could not create identities directory: %v\n", err)
		}
		if err := id.ToFile(idPath); err != nil {
			log.Fatalf("Could not save identity %q: %v\n", idPath, err)
		}
	}
	return id
}

func decodeRequestPayload(data []byte) (string, float64, *int, *int, []byte, error) {
	unpacked, err := rns.Unpack(data)
	if err != nil {
		return "", 0, nil, nil, nil, err
	}
	parts, ok := unpacked.([]any)
	if !ok || len(parts) < 5 {
		return "", 0, nil, nil, nil, errors.New("malformed request payload")
	}

	cmdBytes, ok := parts[0].([]byte)
	if !ok {
		return "", 0, nil, nil, nil, errors.New("malformed command in payload")
	}

	var timeout float64
	switch v := parts[1].(type) {
	case float64:
		timeout = v
	case int64:
		timeout = float64(v)
	default:
	}

	var stdoutLimit *int
	if v, ok := utils.AsInt(parts[2]); ok {
		stdoutLimit = &v
	}

	var stderrLimit *int
	if v, ok := utils.AsInt(parts[3]); ok {
		stderrLimit = &v
	}

	stdinBytes, _ := parts[4].([]byte)

	return string(cmdBytes), timeout, stdoutLimit, stderrLimit, stdinBytes, nil
}

func (rt *runtimeT) doListen(ts rns.Transport) {
	app := rt.app
	logger := rt.logger

	id := rt.prepareIdentity(app.identityPath)

	dest, err := rns.NewDestination(ts, id, rns.DestinationIn, rns.DestinationSingle, AppName, "execute")
	if err != nil {
		log.Fatalf("Could not create destination: %v\n", err)
	}

	if app.printIdentity {
		fmt.Printf("Identity     : %v\n", id)
		fmt.Printf("Listening on : %v\n", rns.PrettyHexRep(dest.Hash))
		return
	}

	// Load allowed identities
	var allowedIdentityHashes [][]byte
	if app.allowedHashes != nil {
		for _, a := range app.allowedHashes {
			destLen := (rns.TruncatedHashLength / 8) * 2
			if len(a) != destLen {
				fmt.Printf("Allowed destination length is invalid, must be %v hexadecimal characters (%v bytes).\n", destLen, destLen/2)
				os.Exit(1)
			}
			destinationHash, err := rns.HexToBytes(a)
			if err != nil {
				fmt.Println("Invalid destination entered. Check your input.")
				os.Exit(1)
			}
			allowedIdentityHashes = append(allowedIdentityHashes, destinationHash)
		}
	}

	home, _ := os.UserHomeDir()
	allowedFilePath := resolveAllowedIdentitiesPath(home)

	if allowedFilePath != "" {
		if data, err := os.ReadFile(allowedFilePath); err == nil {
			allowedByFile := strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n")
			for _, allowedID := range allowedByFile {
				destLen := (rns.TruncatedHashLength / 8) * 2
				if len(allowedID) == destLen {
					if destinationHash, err := rns.HexToBytes(allowedID); err == nil {
						allowedIdentityHashes = append(allowedIdentityHashes, destinationHash)
					}
				}
			}
		} else {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	if len(allowedIdentityHashes) < 1 && !app.noAuth {
		fmt.Println("Warning: No allowed identities configured, rncx will not accept any commands!")
	}

	dest.SetLinkEstablishedCallback(func(link *rns.Link) {
		logger.Info("Command link %v established", link)
		link.SetRemoteIdentifiedCallback(func(link *rns.Link, identity *rns.Identity) {
			logger.Info("Initiator of link %v identified as %v", link, rns.PrettyHexRep(identity.Hash))
			if !app.noAuth {
				allowed := false
				for _, hash := range allowedIdentityHashes {
					if bytes.Equal(hash, identity.Hash) {
						allowed = true
						break
					}
				}
				if !allowed {
					logger.Info("Identity %v not allowed, tearing down link", rns.PrettyHexRep(identity.Hash))
					link.Teardown()
				}
			}
		})
		link.SetLinkClosedCallback(func(link *rns.Link) {
			logger.Info("Command link %v closed", link)
		})
	})

	policy := rns.AllowList
	if app.noAuth {
		policy = rns.AllowAll
	}

	dest.RegisterRequestHandler("command", rt.handleCommandRequest, policy, allowedIdentityHashes, true)

	logger.Info("rnx listening for commands on %v", rns.PrettyHexRep(dest.Hash))

	if !app.noAnnounce {
		_ = dest.Announce(nil)
	}

	// Keep alive
	select {}
}

func (rt *runtimeT) handleCommandRequest(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	logger := rt.logger

	cmdStr, timeout, stdoutLimit, stderrLimit, stdinBytes, err := decodeRequestPayload(data)
	if err != nil {
		logger.Warning("Failed to decode command request: %v", err)
		return nil
	}

	if remoteIdentity != nil {
		logger.Info("Executing command [%v] for %v", cmdStr, rns.PrettyHexRep(remoteIdentity.Hash))
	} else {
		logger.Info("Executing command [%v] for unknown requestor", cmdStr)
	}

	// result: [executed, returncode, stdout, stderr, stdout_len, stderr_len, started, concluded]
	result := []any{
		false,                                // 0: Command was executed
		nil,                                  // 1: Return value
		nil,                                  // 2: Stdout
		nil,                                  // 3: Stderr
		nil,                                  // 4: Total stdout length
		nil,                                  // 5: Total stderr length
		float64(time.Now().UnixNano()) / 1e9, // 6: Started
		nil,                                  // 7: Concluded
	}

	tokens, err := utils.ShlexSplit(cmdStr)
	if err != nil {
		return result
	}
	if len(tokens) == 0 {
		return result
	}

	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout*float64(time.Second)))
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, tokens[0], tokens[1:]...)
	if len(stdinBytes) > 0 {
		cmd.Stdin = bytes.NewReader(stdinBytes)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		result[0] = false
		return result
	}
	result[0] = true
	err = cmd.Wait()

	concludedAt := float64(time.Now().UnixNano()) / 1e9
	result[7] = concludedAt

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result[1] = int64(exitErr.ExitCode())
		} else {
			result[1] = int64(-1)
		}
	} else {
		result[1] = int64(0)
	}

	stdoutBytes := stdout.Bytes()
	stderrBytes := stderr.Bytes()
	result[4] = int64(len(stdoutBytes))
	result[5] = int64(len(stderrBytes))

	if stdoutLimit != nil && len(stdoutBytes) > *stdoutLimit {
		if *stdoutLimit == 0 {
			result[2] = []byte{}
		} else {
			result[2] = append([]byte{}, stdoutBytes[:*stdoutLimit]...)
		}
	} else {
		result[2] = append([]byte{}, stdoutBytes...)
	}

	if stderrLimit != nil && len(stderrBytes) > *stderrLimit {
		if *stderrLimit == 0 {
			result[3] = []byte{}
		} else {
			result[3] = append([]byte{}, stderrBytes[:*stderrLimit]...)
		}
	} else {
		result[3] = append([]byte{}, stderrBytes...)
	}

	return result
}

type requestLink interface {
	Request(path string, data any, responseCallback, failedCallback, progressCallback func(*rns.RequestReceipt), timeout time.Duration) (*rns.RequestReceipt, error)
}

func (rt *runtimeT) packExecuteRequestPayload(command string) ([]byte, error) {
	app := rt.app

	var stdoutL, stderrL any
	if app.stdoutLimit >= 0 {
		stdoutL = int64(app.stdoutLimit)
	}
	if app.stderrLimit >= 0 {
		stderrL = int64(app.stderrLimit)
	}

	payload := []any{
		[]byte(command),
		app.timeout,
		stdoutL,
		stderrL,
		[]byte(app.stdin),
	}

	return rns.Pack(payload)
}

func (rt *runtimeT) requestExecute(link requestLink, command string, responseCallback, failedCallback func(*rns.RequestReceipt)) error {
	_, err := rt.requestExecuteWithProgress(link, command, responseCallback, failedCallback, nil)
	return err
}

func (rt *runtimeT) requestExecuteWithProgress(link requestLink, command string, responseCallback, failedCallback, progressCallback func(*rns.RequestReceipt)) (*rns.RequestReceipt, error) {
	packedPayload, err := rt.packExecuteRequestPayload(command)
	if err != nil {
		return nil, err
	}

	return link.Request("command", packedPayload, responseCallback, failedCallback, progressCallback, time.Duration(rt.app.timeout*float64(time.Second)))
}

func (rt *runtimeT) successExitCode(returnValue any) int {
	if !rt.app.mirror {
		return 0
	}

	retval, ok := utils.AsInt(returnValue)
	if !ok {
		return 240
	}
	return retval
}

func (rt *runtimeT) doExecute(ts rns.Transport, destHashHex string, command string) {
	app := rt.app
	logger := rt.logger

	destHash, err := rns.HexToBytes(destHashHex)
	if err != nil {
		log.Fatalf("Invalid destination hash %q: %v\n", destHashHex, err)
	}

	id := rt.prepareIdentity(app.identityPath)

	remoteID := rns.RecallIdentity(ts, destHash)
	if remoteID == nil {
		if err := ts.RequestPath(destHash); err != nil {
			log.Fatalf("Could not request path to %v: %v\n", rns.PrettyHexRep(destHash), err)
		}
		// Wait for path
		deadline := time.Now().Add(time.Duration(app.timeout * float64(time.Second)))
		for time.Now().Before(deadline) {
			if ts.HasPath(destHash) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		remoteID = rns.RecallIdentity(ts, destHash)
	}

	if remoteID == nil {
		log.Fatalf("Could not recall identity for %v\n", rns.PrettyHexRep(destHash))
	}

	dest, err := rns.NewDestination(ts, remoteID, rns.DestinationOut, rns.DestinationSingle, AppName, "execute")
	if err != nil {
		log.Fatalf("Could not create destination: %v\n", err)
	}

	link, err := rns.NewLink(ts, dest)
	if err != nil {
		log.Fatalf("Could not establish link to %v: %v\n", rns.PrettyHexRep(destHash), err)
	}

	linkEstablished := make(chan struct{})
	link.SetLinkEstablishedCallback(func(l *rns.Link) {
		logger.Info("Link to %v established", rns.PrettyHexRep(destHash))
		close(linkEstablished)
	})

	if err := link.Establish(); err != nil {
		log.Fatalf("Could not start link establishment: %v\n", err)
	}

	// Wait for link establishment
	select {
	case <-linkEstablished:
		// OK
	case <-time.After(time.Duration(app.timeout * float64(time.Second))):
		log.Fatalf("Link establishment timed out\n")
	}

	if !app.noID {
		_ = link.Identify(id)
	}

	done := make(chan struct{})
	failed := make(chan struct{})
	receiving := make(chan struct{})
	closeOnce := func(ch chan struct{}) {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
	_, err = rt.requestExecuteWithProgress(link, command, func(receipt *rns.RequestReceipt) {
		rt.handleResponse(receipt.RequestID, receipt.Response)
		closeOnce(done)
	}, func(receipt *rns.RequestReceipt) {
		closeOnce(failed)
	}, func(receipt *rns.RequestReceipt) {
		if receipt.GetStatus() == rns.RequestReceiving {
			closeOnce(receiving)
		}
	})
	if err != nil {
		log.Fatalf("Could not send request: %v\n", err)
	}

	resultTimeout := time.Duration((app.timeout + 10.0) * float64(time.Second))
	if app.resultTimeout > 0 {
		resultTimeout = time.Duration(app.resultTimeout * float64(time.Second))
	}

	select {
	case <-done:
		return
	case <-failed:
		fmt.Println("No result was received")
		os.Exit(245)
	case <-receiving:
		select {
		case <-done:
			return
		case <-failed:
			fmt.Println("Receiving result failed")
			os.Exit(246)
		case <-time.After(resultTimeout):
			fmt.Println("Receiving result failed")
			os.Exit(246)
		}
	case <-time.After(time.Duration(app.timeout * float64(time.Second))):
		log.Fatalf("Initiator timed out waiting for response\n")
	}
}

func (rt *runtimeT) handleResponse(requestID []byte, response any) {
	app := rt.app

	result, ok := response.([]any)
	if !ok || len(result) < 8 {
		fmt.Println("Received invalid result")
		os.Exit(247)
	}

	executed, _ := result[0].(bool)
	stdout, _ := result[2].([]byte)
	stderr, _ := result[3].([]byte)
	stdoutLen, _ := utils.AsInt(result[4])
	stderrLen, _ := utils.AsInt(result[5])
	started, _ := result[6].(float64)
	concluded, _ := result[7].(float64)

	if executed {
		if app.detailed {
			if len(stdout) > 0 {
				_, _ = os.Stdout.Write(stdout)
			}
			if len(stderr) > 0 {
				_, _ = os.Stderr.Write(stderr)
			}

			_ = os.Stdout.Sync()
			_ = os.Stderr.Sync()

			fmt.Println("\n--- End of remote output, rnx done ---")
			if started != 0 && concluded != 0 {
				cmdDuration := concluded - started
				fmt.Printf("Remote command execution took %.3f seconds\n", cmdDuration)
			}

			if stdout != nil {
				if len(stdout) < stdoutLen {
					fmt.Printf("Remote wrote %v bytes to stdout, %v bytes displayed\n", stdoutLen, len(stdout))
				} else {
					fmt.Printf("Remote wrote %v bytes to stdout\n", stdoutLen)
				}
			}

			if stderr != nil {
				if len(stderr) < stderrLen {
					fmt.Printf("Remote wrote %v bytes to stderr, %v bytes displayed\n", stderrLen, len(stderr))
				} else {
					fmt.Printf("Remote wrote %v bytes to stderr\n", stderrLen)
				}
			}

		} else {
			if len(stdout) > 0 {
				_, _ = os.Stdout.Write(stdout)
			}
			if len(stderr) > 0 {
				_, _ = os.Stderr.Write(stderr)
			}
			_ = os.Stdout.Sync()
			_ = os.Stderr.Sync()

			if (app.stdoutLimit != 0 && len(stdout) < stdoutLen) || (app.stderrLimit != 0 && len(stderr) < stderrLen) {
				fmt.Println("\nOutput truncated before being returned:")
				if len(stdout) != 0 && len(stdout) < stdoutLen {
					fmt.Printf("  stdout truncated to %v bytes\n", len(stdout))
				}
				if len(stderr) != 0 && len(stderr) < stderrLen {
					fmt.Printf("  stderr truncated to %v bytes\n", len(stderr))
				}
			}
		}

		os.Exit(rt.successExitCode(result[1]))
	} else {
		fmt.Println("Remote could not execute command")
		os.Exit(248)
	}
}

func (rt *runtimeT) doInteractive(ts rns.Transport, destHashHex string) {
	app := rt.app
	logger := rt.logger

	destHash, err := rns.HexToBytes(destHashHex)
	if err != nil {
		log.Fatalf("Invalid destination hash %q: %v\n", destHashHex, err)
	}

	id := rt.prepareIdentity(app.identityPath)

	remoteID := rns.RecallIdentity(ts, destHash)
	if remoteID == nil {
		if err := ts.RequestPath(destHash); err != nil {
			log.Fatalf("Could not request path to %v: %v\n", rns.PrettyHexRep(destHash), err)
		}
		// Wait for path
		deadline := time.Now().Add(time.Duration(app.timeout * float64(time.Second)))
		for time.Now().Before(deadline) {
			if ts.HasPath(destHash) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		remoteID = rns.RecallIdentity(ts, destHash)
	}

	if remoteID == nil {
		log.Fatalf("Could not recall identity for %v\n", rns.PrettyHexRep(destHash))
	}

	dest, err := rns.NewDestination(ts, remoteID, rns.DestinationOut, rns.DestinationSingle, AppName, "execute")
	if err != nil {
		log.Fatalf("Could not create destination: %v\n", err)
	}

	link, err := rns.NewLink(ts, dest)
	if err != nil {
		log.Fatalf("Could not establish link to %v: %v\n", rns.PrettyHexRep(destHash), err)
	}

	linkEstablished := make(chan struct{})
	link.SetLinkEstablishedCallback(func(l *rns.Link) {
		logger.Info("Link to %v established", rns.PrettyHexRep(destHash))
		close(linkEstablished)
	})

	if err := link.Establish(); err != nil {
		log.Fatalf("Could not start link establishment: %v\n", err)
	}

	// Wait for link establishment
	select {
	case <-linkEstablished:
		// OK
	case <-time.After(time.Duration(app.timeout * float64(time.Second))):
		log.Fatalf("Link establishment timed out\n")
	}

	if !app.noID {
		_ = link.Identify(id)
	}

	scanner := bufio.NewScanner(os.Stdin)
	prompt := "> "
	fmt.Print(prompt)
	for scanner.Scan() {
		command := scanner.Text()
		commandLower := strings.ToLower(command)
		if commandLower == "quit" || commandLower == "exit" {
			os.Exit(0)
		}
		if commandLower == "clear" {
			fmt.Print("\033c")
		} else if command != "" {
			// Payload: [command_bytes, timeout, stdout_limit, stderr_limit, stdin_bytes]
			var stdoutL, stderrL any
			if app.stdoutLimit >= 0 {
				stdoutL = int64(app.stdoutLimit)
			}
			if app.stderrLimit >= 0 {
				stderrL = int64(app.stderrLimit)
			}

			payload := []any{
				[]byte(command),
				app.timeout,
				stdoutL,
				stderrL,
				[]byte(app.stdin),
			}

			packedPayload, err := rns.Pack(payload)
			if err != nil {
				fmt.Printf("Could not pack request payload: %v\n", err)
				continue
			}

			done := make(chan bool)
			_, err = link.Request("command", packedPayload, func(receipt *rns.RequestReceipt) {
				rt.handleResponseInteractive(receipt.Response)
				done <- true
			}, func(receipt *rns.RequestReceipt) {
				fmt.Println("Request failed")
				done <- true
			}, nil, time.Duration(app.timeout*float64(time.Second)))
			if err != nil {
				fmt.Printf("Could not send request: %v\n", err)
			} else {
				<-done
			}
		}
		fmt.Print(prompt)
	}
}

func (rt *runtimeT) handleResponseInteractive(response any) {
	app := rt.app

	result, ok := response.([]any)
	if !ok || len(result) < 8 {
		fmt.Println("No result was received or malformed result")
		return
	}

	executed, _ := result[0].(bool)
	_, _ = utils.AsInt(result[1])
	stdout, _ := result[2].([]byte)
	stderr, _ := result[3].([]byte)
	stdoutLen, _ := utils.AsInt(result[4])
	stderrLen, _ := utils.AsInt(result[5])
	started, _ := result[6].(float64)
	concluded, _ := result[7].(float64)

	if executed {
		if len(stdout) > 0 {
			_, _ = os.Stdout.Write(stdout)
		}
		if len(stderr) > 0 {
			_, _ = os.Stderr.Write(stderr)
		}

		if app.detailed {
			_ = os.Stdout.Sync()
			_ = os.Stderr.Sync()

			fmt.Println("\n--- End of remote output, rnx done ---")
			if started != 0 && concluded != 0 {
				cmdDuration := concluded - started
				fmt.Printf("Remote command execution took %.3f seconds\n", cmdDuration)
			}

			if stdout != nil {
				if len(stdout) < stdoutLen {
					fmt.Printf("Remote wrote %v bytes to stdout, %v bytes displayed\n", stdoutLen, len(stdout))
				} else {
					fmt.Printf("Remote wrote %v bytes to stdout\n", stdoutLen)
				}
			}

			if stderr != nil {
				if len(stderr) < stderrLen {
					fmt.Printf("Remote wrote %v bytes to stderr, %v bytes displayed\n", stderrLen, len(stderr))
				} else {
					fmt.Printf("Remote wrote %v bytes to stderr\n", stderrLen)
				}
			}
			fmt.Println("---------------------------------")
		} else {
			_ = os.Stdout.Sync()
			_ = os.Stderr.Sync()
		}
	} else {
		fmt.Println("Command could not be executed on remote host")
	}
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
