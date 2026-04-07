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
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

const (
	appName            = "rnsh"
	defaultServiceName = "default"
)

var nonWordRE = regexp.MustCompile(`\W+`)

type announcer interface {
	Announce([]byte) error
}

type announcementTicker interface {
	C() <-chan time.Time
	Stop()
}

type realAnnouncementTicker struct {
	ticker *time.Ticker
}

func (t *realAnnouncementTicker) C() <-chan time.Time { return t.ticker.C }
func (t *realAnnouncementTicker) Stop()               { t.ticker.Stop() }

var newAnnouncementTicker = func(interval time.Duration) announcementTicker {
	return &realAnnouncementTicker{ticker: time.NewTicker(interval)}
}

func (rt *runtimeT) configureLogger(verbose, quiet int) {
	rt.logger = rns.NewLogger()
	// rnsh follows the Python baseline of LogInfo here, which differs from
	// the other cmd/* tools in this repository that start from LogNotice.
	level := rns.LogInfo + verbose - quiet
	if level < rns.LogCritical {
		level = rns.LogCritical
	}
	if level > rns.LogDebug {
		level = rns.LogDebug
	}
	rt.logger.SetLogLevel(level)
}

type runtimeT struct {
	opts   options
	logger *rns.Logger
}

func newRuntime(opts options) *runtimeT {
	rt := &runtimeT{opts: opts}
	rt.configureLogger(opts.verbose, opts.quiet)
	return rt
}

func main() {
	log.SetFlags(0)
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	_ = stdin

	usageOutput := io.Discard
	if hasHelpFlag(args) {
		usageOutput = stdout
	}

	opts, err := parseFlags(args, usageOutput)
	if err != nil {
		if err == errHelp {
			return 0
		}
		_, _ = fmt.Fprintln(stderr, err)
		usage(stderr)
		return 2
	}

	if opts.version {
		_, _ = fmt.Fprintf(stdout, "gornsh %v\n", rns.VERSION)
		return 0
	}

	rt := newRuntime(opts)

	if rt.opts.printIdentity {
		if err := rt.printIdentity(); err != nil {
			log.Fatalf("gornsh: %v", err)
		}
		return 0
	}

	if rt.opts.listen {
		if err := rt.doListen(); err != nil {
			log.Fatalf("gornsh: %v", err)
		}
		return 0
	}

	if rt.opts.destination == "" {
		usage(stderr)
		return 2
	}

	code, err := rt.doInitiate()
	if err != nil {
		log.Fatalf("gornsh: %v", err)
	}
	return code
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func (rt *runtimeT) printIdentity() error {
	opts := rt.opts
	ts := rns.NewTransportSystem(rt.logger)
	ret, err := rns.NewReticulum(ts, opts.configDir)
	if err != nil {
		return fmt.Errorf("could not initialize Reticulum: %w", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			rt.logger.Warning("Could not close Reticulum properly: %v", err)
		}
	}()

	identityPath, err := resolveIdentityPath(opts)
	if err != nil {
		return err
	}

	id, err := rt.loadOrCreateIdentity(identityPath)
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
		_, _ = fmt.Printf("Listening on : %v\n", rns.PrettyHex(destination.Hash))
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
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	logServiceName(logger, opts.serviceName)
	_, _ = fmt.Println(listeningReadyLine())
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulum(ts, opts.configDir)
	if err != nil {
		return fmt.Errorf("could not initialize Reticulum: %w", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			rt.logger.Warning("Could not close Reticulum properly: %v", err)
		}
	}()

	identityPath, err := resolveIdentityPath(opts)
	if err != nil {
		return err
	}

	id, err := rt.loadOrCreateIdentity(identityPath)
	if err != nil {
		return err
	}

	destination, err := rns.NewDestination(ts, id, rns.DestinationIn, rns.DestinationSingle, appName)
	if err != nil {
		return fmt.Errorf("could not create destination: %w", err)
	}

	allowMode, allowedList := rt.buildAllowPolicy(opts)
	destination.SetLinkEstablishedCallback(func(link *rns.Link) {
		rt.wireListenerChannelSession(link, opts, allowedList)
	})
	destination.RegisterRequestHandler("command", func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
		if !opts.noAuth && remoteIdentity == nil {
			rt.logger.Warning("Rejected unauthenticated command request")
			return nil
		}

		if !opts.noAuth && remoteIdentity != nil && len(allowedList) > 0 && !identityAllowed(remoteIdentity.Hash, allowedList) {
			rt.logger.Warning("Rejected unauthorized command request from %v", remoteIdentity.HexHash)
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

	_, _ = fmt.Fprintln(os.Stdout, listeningDestinationLine(destination.Hash))
	stopAnnouncements := startAnnouncements(destination, opts.announceEvery, rt.logger)
	defer stopAnnouncements()

	<-sigCh
	logger.Info("Shutting down")
	return nil
}

func startAnnouncements(destination announcer, announceEvery *int, logger *rns.Logger) func() {
	if announceEvery == nil {
		return func() {}
	}

	if err := destination.Announce(nil); err != nil && logger != nil {
		logger.Warning("Initial announce failed: %v", err)
	}

	if *announceEvery <= 0 {
		return func() {}
	}

	ticker := newAnnouncementTicker(time.Duration(*announceEvery) * time.Second)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C():
				if err := destination.Announce(nil); err != nil && logger != nil {
					logger.Warning("Periodic announce failed: %v", err)
				}
			}
		}
	}()

	return func() {
		close(done)
		ticker.Stop()
	}
}

func logServiceName(logger *rns.Logger, serviceName string) {
	if logger == nil || serviceName == "" {
		return
	}
	logger.Info("Using service name %v", serviceName)
}

func listeningReadyLine() string {
	return "rnsh listening..."
}

func listeningDestinationLine(hash []byte) string {
	return fmt.Sprintf("rnsh listening for commands on %v", rns.PrettyHex(hash))
}

func (rt *runtimeT) doInitiate() (int, error) {
	opts := rt.opts
	if rt.logger == nil {
		rt.logger = rns.NewLogger()
	}
	ts := rns.NewTransportSystem(rt.logger)
	ret, err := rns.NewReticulumWithLogger(ts, opts.configDir, rt.logger)
	if err != nil {
		return 1, fmt.Errorf("could not initialize Reticulum: %w", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			rt.logger.Warning("Could not close Reticulum properly: %v", err)
		}
	}()

	identityPath, err := resolveIdentityPath(opts)
	if err != nil {
		return 1, err
	}

	id, err := rt.loadOrCreateIdentity(identityPath)
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
		time.Sleep(50 * time.Millisecond)
		if err := link.Identify(id); err != nil {
			return 1, fmt.Errorf("identify failed: %w", err)
		}
	}

	tty, err := newTTYRestorer(int(os.Stdin.Fd()))
	if err != nil {
		return 1, err
	}
	if !opts.noTTY {
		if err := tty.raw(); err != nil {
			return 1, err
		}
		defer func() {
			if err := tty.restore(); err != nil {
				rt.logger.Warning("Could not restore terminal mode: %v", err)
			}
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	watcher, stopWatcher := startInitiatorShutdownWatcher(sigCh, link.Teardown)
	defer stopWatcher()

	code, err := rt.runInitiatorChannelSession(link, opts)
	if watcher.requested() {
		return 1, nil
	}
	return code, err
}

type initiatorShutdownWatcher struct {
	mu                sync.Mutex
	shutdownRequested bool
}

func (w *initiatorShutdownWatcher) requested() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.shutdownRequested
}

func startInitiatorShutdownWatcher(sigCh <-chan os.Signal, teardown func()) (*initiatorShutdownWatcher, func()) {
	watcher := &initiatorShutdownWatcher{}
	doneCh := make(chan struct{})
	go func() {
		select {
		case <-sigCh:
			watcher.mu.Lock()
			watcher.shutdownRequested = true
			watcher.mu.Unlock()
			if teardown != nil {
				teardown()
			}
		case <-doneCh:
		}
	}()
	stop := func() {
		close(doneCh)
	}
	return watcher, stop
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

func (rt *runtimeT) buildAllowPolicy(opts options) (int, [][]byte) {
	if opts.noAuth {
		return rns.AllowAll, nil
	}

	hashes := append([]string{}, opts.allowHashes...)
	hashes = append(hashes, readAllowedHashesFromDefaultFiles()...)
	allowedList := make([][]byte, 0, len(hashes))
	for _, hash := range hashes {
		decoded, ok := parseAllowedIdentityHash(hash)
		if !ok {
			rt.logger.Warning("Ignoring invalid allowed identity hash %q", hash)
			continue
		}
		allowedList = append(allowedList, decoded)
	}

	if len(allowedList) == 0 {
		rt.logger.Warning("Authentication enabled but no allowed identities configured; denying all command requests")
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

func (rt *runtimeT) loadOrCreateIdentity(identityPath string) (*rns.Identity, error) {
	id, err := rns.FromFile(identityPath, rt.logger)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("could not load identity %q: %w", identityPath, err)
	}

	if mkErr := os.MkdirAll(filepath.Dir(identityPath), 0o700); mkErr != nil {
		return nil, fmt.Errorf("could not create identity directory: %w", mkErr)
	}

	id, err = rns.NewIdentity(true, rt.logger)
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
