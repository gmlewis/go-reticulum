// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gornsh provides Reticulum remote shell sessions compatible with rnsh.
//
// It supports listener and initiator roles over the rnsh application namespace,
// including identity handling, optional authentication controls, mirror-exit
// behavior, timeout control, and command execution modes.
//
// Usage:
//
//	Listener identity/destination info:
//	  gornsh -l [-c <configdir>] [-i <identityfile> | -s <service_name>] -p
//
//	Initiator identity info:
//	  gornsh [-c <configdir>] [-i <identityfile>] -p
//
//	Initiate remote session/command:
//	  gornsh [-c <configdir>] [-i <identityfile>] <destination_hash> [--] [program [args ...]]
//
// Key flags:
//
//	-l, --listen                 Listen mode (server side)
//	-i, --identity <file>        Identity path
//	-c, --config <dir>           Reticulum config directory
//	-s, --service <name>         Listener service identity suffix
//	-a, --allowed <hash>         Allowed remote identity hash (repeatable)
//	-n, --no-auth                Disable listener-side auth checks
//	-b, --announce <seconds>     Announce once (0) or periodically
//	-m, --mirror                 Mirror remote process exit code
//	-w, --timeout <seconds>      Initiator connect/request timeout
//	-p, --print-identity         Print identity and (listener) destination
//	-v, --verbose / -q, --quiet  Logging level controls
package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

const (
	appName            = "rnsh"
	defaultServiceName = "default"
)

var nonWordRE = regexp.MustCompile(`\W+`)

func configureLogger(verbose, quiet bool) *rns.Logger {
	logger := rns.NewLogger()
	if verbose {
		logger.SetLogLevel(rns.LogVerbose)
	}
	if quiet {
		logger.SetLogLevel(rns.LogWarning)
	}
	return logger
}

type runtimeT struct {
	opts   options
	logger *rns.Logger
}

func newRuntime(opts options) *runtimeT {
	return &runtimeT{opts: opts, logger: configureLogger(opts.verbose, opts.quiet)}
}

func main() {
	opts, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		if err == errHelp {
			return
		}
		log.Fatalf("gornsh: %v", err)
	}

	if opts.version {
		_, _ = fmt.Printf("gornsh %v\n", rns.VERSION)
		return
	}

	rt := newRuntime(opts)

	if rt.opts.printIdentity {
		if err := rt.printIdentity(); err != nil {
			log.Fatalf("gornsh: %v", err)
		}
		return
	}

	if rt.opts.listen {
		if err := rt.doListen(); err != nil {
			log.Fatalf("gornsh: %v", err)
		}
		return
	}

	if rt.opts.destination == "" {
		usage(os.Stderr)
		os.Exit(2)
	}

	code, err := rt.doInitiate()
	if err != nil {
		log.Fatalf("gornsh: %v", err)
	}
	os.Exit(code)
}

func (rt *runtimeT) printIdentity() error {
	opts := rt.opts
	ts := rns.NewTransportSystem()
	ret, err := rns.NewReticulum(ts, opts.configDir)
	if err != nil {
		return fmt.Errorf("could not initialize Reticulum: %w", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			rns.Logf("Warning: Could not close Reticulum properly: %v", rns.LogWarning, false, err)
		}
	}()

	identityPath, err := resolveIdentityPath(opts)
	if err != nil {
		return err
	}

	id, err := loadOrCreateIdentity(identityPath)
	if err != nil {
		return err
	}

	if opts.listen && opts.serviceName != "" {
		_, _ = fmt.Printf("Using service name %q\n", opts.serviceName)
	}
	_, _ = fmt.Printf("Identity     : %v\n", id.HexHash)

	if opts.listen {
		destination, err := rns.NewDestination(ts, id, rns.DestinationIn, rns.DestinationSingle, appName)
		if err != nil {
			return fmt.Errorf("could not create destination: %w", err)
		}
		_, _ = fmt.Printf("Listening on : %x\n", destination.Hash)
	}

	return nil
}

func resolveIdentityPath(opts options) (string, error) {
	if opts.identityPath != "" {
		return opts.identityPath, nil
	}

	configDir := opts.configDir
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("could not determine home directory: %w", err)
		}
		configDir = filepath.Join(home, ".reticulum")
	}

	identityName := appName
	if opts.listen && opts.serviceName != "" {
		identityName = identityName + "." + opts.serviceName
	}

	return filepath.Join(configDir, "storage", "identities", identityName), nil
}

func (rt *runtimeT) doListen() error {
	opts := rt.opts
	logger := rt.logger
	if logger == nil {
		logger = rns.NewLogger()
	}
	ts := rns.NewTransportSystem()
	ret, err := rns.NewReticulum(ts, opts.configDir)
	if err != nil {
		return fmt.Errorf("could not initialize Reticulum: %w", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			rns.Logf("Warning: Could not close Reticulum properly: %v", rns.LogWarning, false, err)
		}
	}()

	identityPath, err := resolveIdentityPath(opts)
	if err != nil {
		return err
	}

	id, err := loadOrCreateIdentity(identityPath)
	if err != nil {
		return err
	}

	destination, err := rns.NewDestination(ts, id, rns.DestinationIn, rns.DestinationSingle, appName)
	if err != nil {
		return fmt.Errorf("could not create destination: %w", err)
	}

	allowMode, allowedList := buildAllowPolicy(logger, opts)
	destination.SetLinkEstablishedCallback(func(link *rns.Link) {
		wireListenerChannelSession(link, opts, allowedList)
	})
	destination.RegisterRequestHandler("command", func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
		if !opts.noAuth && remoteIdentity == nil {
			rns.Log("Rejected unauthenticated command request", rns.LogWarning, false)
			return nil
		}

		if !opts.noAuth && remoteIdentity != nil && len(allowedList) > 0 && !identityAllowed(remoteIdentity.Hash, allowedList) {
			rns.Logf("Rejected unauthorized command request from %v", rns.LogWarning, false, remoteIdentity.HexHash)
			return nil
		}

		remoteCommand := decodeRemoteCommand(data)
		commandToRun, err := chooseCommand(opts, remoteCommand)
		if err != nil {
			return []any{false, int64(126), []byte{}, []byte(err.Error()), int64(0), int64(len(err.Error())), float64(time.Now().UnixNano()) / 1e9, float64(time.Now().UnixNano()) / 1e9}
		}

		started := time.Now()
		retval, stdout, stderr := executeCommand(commandToRun, remoteIdentity)
		concluded := time.Now()

		return []any{true, int64(retval), stdout, stderr, int64(len(stdout)), int64(len(stderr)), float64(started.UnixNano()) / 1e9, float64(concluded.UnixNano()) / 1e9}
	}, allowMode, allowedList, true)

	_, _ = fmt.Printf("rnsh listening on %x\n", destination.Hash)

	if opts.announceEvery >= 0 {
		if err := destination.Announce(nil); err != nil {
			rns.Logf("Initial announce failed: %v", rns.LogWarning, false, err)
		}
	}

	if opts.announceEvery > 0 {
		interval := time.Duration(opts.announceEvery) * time.Second
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for range ticker.C {
				if err := destination.Announce(nil); err != nil {
					rns.Logf("Periodic announce failed: %v", rns.LogWarning, false, err)
				}
			}
		}()
	}

	select {}
}

func (rt *runtimeT) doInitiate() (int, error) {
	opts := rt.opts
	ts := rns.NewTransportSystem()
	if rt.logger == nil {
		rt.logger = rns.NewLogger()
	}
	ret, err := rns.NewReticulumWithLogger(ts, opts.configDir, rt.logger)
	if err != nil {
		return 1, fmt.Errorf("could not initialize Reticulum: %w", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			rt.logger.Log(fmt.Sprintf("Warning: Could not close Reticulum properly: %v", err), rns.LogWarning, false)
		}
	}()

	identityPath, err := resolveIdentityPath(opts)
	if err != nil {
		return 1, err
	}

	id, err := loadOrCreateIdentity(identityPath)
	if err != nil {
		return 1, err
	}

	destHash, err := rns.HexToBytes(strings.TrimSpace(opts.destination))
	if err != nil {
		return 1, fmt.Errorf("invalid destination hash %q: %w", opts.destination, err)
	}

	remoteIdentity, err := resolveRemoteIdentity(ret.Transport(), destHash, time.Duration(opts.timeoutSec)*time.Second)
	if err != nil {
		return 1, err
	}

	remoteDest, err := rns.NewDestination(ts, remoteIdentity, rns.DestinationOut, rns.DestinationSingle, appName)
	if err != nil {
		return 1, fmt.Errorf("could not create remote destination: %w", err)
	}

	link, err := rns.NewLink(ts, remoteDest)
	if err != nil {
		return 1, fmt.Errorf("could not create link: %w", err)
	}

	established := make(chan struct{}, 1)
	link.SetLinkEstablishedCallback(func(l *rns.Link) {
		established <- struct{}{}
	})
	if err := link.Establish(); err != nil {
		return 1, fmt.Errorf("could not establish link: %w", err)
	}

	select {
	case <-established:
	case <-time.After(time.Duration(opts.timeoutSec) * time.Second):
		return 1, errors.New("link establishment timed out")
	}

	if !opts.noID {
		if err := link.Identify(id); err != nil {
			return 1, fmt.Errorf("identify failed: %w", err)
		}
	}

	return runInitiatorChannelSession(link, opts)
}

func resolveRemoteIdentity(ts rns.Transport, destHash []byte, timeout time.Duration) (*rns.Identity, error) {
	remoteIdentity := rns.RecallIdentity(ts, destHash)
	if remoteIdentity != nil {
		return remoteIdentity, nil
	}

	if err := ts.RequestPath(destHash); err != nil {
		return nil, fmt.Errorf("could not request path to %x: %w", destHash, err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		remoteIdentity = rns.RecallIdentity(ts, destHash)
		if remoteIdentity != nil {
			return remoteIdentity, nil
		}
	}

	return nil, fmt.Errorf("could not resolve remote identity for destination %x", destHash)
}

func joinCommandArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return strings.Join(args, " ")
}

func parseCommandResponse(response any) (int, []byte, []byte, error) {
	parts, ok := response.([]any)
	if !ok || len(parts) < 4 {
		return 0, nil, nil, fmt.Errorf("invalid command response: %#v", response)
	}

	exitCode, ok := toInt(parts[1])
	if !ok {
		return 0, nil, nil, errors.New("invalid exit code in response")
	}

	stdout, _ := toBytes(parts[2])
	stderr, _ := toBytes(parts[3])

	return exitCode, stdout, stderr, nil
}

func toInt(value any) (int, bool) {
	switch n := value.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case int32:
		return int(n), true
	case uint64:
		return int(n), true
	case uint32:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func toBytes(value any) ([]byte, bool) {
	switch b := value.(type) {
	case []byte:
		return b, true
	case string:
		return []byte(b), true
	default:
		return nil, false
	}
}

func buildAllowPolicy(logger *rns.Logger, opts options) (int, [][]byte) {
	if logger == nil {
		logger = rns.NewLogger()
	}
	if opts.noAuth {
		return rns.AllowAll, nil
	}

	hashes := append([]string{}, opts.allowHashes...)
	hashes = append(hashes, readAllowedHashesFromDefaultFiles()...)
	allowedList := make([][]byte, 0, len(hashes))
	for _, hash := range hashes {
		decoded, ok := parseAllowedIdentityHash(hash)
		if !ok {
			logger.Log(fmt.Sprintf("Ignoring invalid allowed identity hash %q", hash), rns.LogWarning, false)
			continue
		}
		allowedList = append(allowedList, decoded)
	}

	if len(allowedList) == 0 {
		logger.Log("Authentication enabled but no allowed identities configured; denying all command requests", rns.LogWarning, false)
	}

	return rns.AllowList, allowedList
}

func parseAllowedIdentityHash(value string) ([]byte, bool) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return nil, false
	}
	decoded, err := rns.HexToBytes(value)
	if err != nil {
		return nil, false
	}
	if len(decoded) != rns.TruncatedHashLength/8 {
		return nil, false
	}
	return decoded, true
}

func readAllowedHashesFromDefaultFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	paths := []string{
		filepath.Join(home, ".config", "rnsh", "allowed_identities"),
		filepath.Join(home, ".rnsh", "allowed_identities"),
	}

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		return splitAllowedFile(string(content))
	}

	return nil
}

func splitAllowedFile(content string) []string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func identityAllowed(remoteHash []byte, allowedList [][]byte) bool {
	for _, allowed := range allowedList {
		if bytes.Equal(remoteHash, allowed) {
			return true
		}
	}
	return false
}

func decodeRemoteCommand(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	unpacked, err := rns.Unpack(data)
	if err != nil {
		return ""
	}

	parts, ok := unpacked.([]any)
	if !ok || len(parts) == 0 {
		return ""
	}

	switch first := parts[0].(type) {
	case []byte:
		return strings.TrimSpace(string(first))
	case string:
		return strings.TrimSpace(first)
	default:
		return ""
	}
}

func chooseCommand(opts options, remoteCommand string) ([]string, error) {
	base := append([]string{}, opts.commandLine...)
	if len(base) == 0 {
		shell := strings.TrimSpace(os.Getenv("SHELL"))
		if shell == "" {
			shell = "/bin/sh"
		}
		base = []string{shell}
	}

	if opts.noRemoteCmd && remoteCommand != "" {
		return nil, errors.New("remote command rejected by listener policy")
	}

	if opts.noRemoteCmd || remoteCommand == "" {
		return base, nil
	}

	if opts.remoteAsArgs {
		return append(base, strings.Fields(remoteCommand)...), nil
	}

	return []string{"/bin/sh", "-lc", remoteCommand}, nil
}

func executeCommand(commandLine []string, remoteIdentity *rns.Identity) (int, []byte, []byte) {
	if len(commandLine) == 0 {
		return 127, nil, []byte("no command to execute")
	}

	cmd := exec.Command(commandLine[0], commandLine[1:]...)
	if remoteIdentity != nil {
		cmd.Env = append(os.Environ(), "RNS_REMOTE_IDENTITY="+remoteIdentity.HexHash)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return 0, stdout.Bytes(), stderr.Bytes()
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), stdout.Bytes(), stderr.Bytes()
	}

	return 127, stdout.Bytes(), []byte(err.Error())
}

func loadOrCreateIdentity(identityPath string) (*rns.Identity, error) {
	id, err := rns.FromFile(identityPath)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("could not load identity %q: %w", identityPath, err)
	}

	if mkErr := os.MkdirAll(filepath.Dir(identityPath), 0o700); mkErr != nil {
		return nil, fmt.Errorf("could not create identity directory: %w", mkErr)
	}

	id, err = rns.NewIdentity(true)
	if err != nil {
		return nil, fmt.Errorf("could not create identity: %w", err)
	}
	if err := id.ToFile(identityPath); err != nil {
		return nil, fmt.Errorf("could not persist identity %q: %w", identityPath, err)
	}

	return id, nil
}

func sanitizeServiceName(serviceName string) string {
	return nonWordRE.ReplaceAllString(serviceName, "")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
