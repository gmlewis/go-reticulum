// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

const tempDirPrefix = "rns-test-"

func closeReticulum(t *testing.T, r *Reticulum) {
	t.Helper()
	if r == nil {
		return
	}
	if err := r.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Errorf("failed to close reticulum: %v", err)
	}
}

func reserveTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserveTCPPort: %v", err)
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		_ = l.Close()
		t.Fatalf("reserveTCPPort unexpected addr type: %T", l.Addr())
	}
	port := addr.Port
	// Hold the probe listener open and hand it to the code under test instead
	// of closing it and hoping the port can be rebound later: between the
	// close and the production bind inside NewReticulum, a parallel test's
	// listener bind or outgoing dial can claim the just-closed ephemeral
	// port, silently misclassifying the instance role (shared/standalone/
	// connected) or failing a server bind. The bind sites adopt the held
	// listener directly (see interfaces pendingTCPListeners); anything never
	// adopted is closed here at test end. This cleanup runs last (LIFO —
	// reserveTCPPort is called before the test's reticulum-cleanup defers are
	// set up), so adopted listeners are already owned by their
	// Reticulum.Close by then.
	interfaces.HoldPendingTCPListener(port, l)
	t.Cleanup(func() { interfaces.ReleasePendingTCPListener(port) })
	return port
}

// newTestTransportSystem creates a minimal TransportSystem for testing.
func newTestTransportSystem(t *testing.T) *TransportSystem {
	t.Helper()
	id := mustTestNewIdentity(t, true)
	ts := NewTransportSystem(nil)
	ts.identity = id
	return ts
}

// newTestPipes creates a pair of connected PipeInterfaces wired to the
// given transport systems and returns a cleanup func.
func newTestPipes(t *testing.T, tsA, tsB *TransportSystem) (*interfaces.PipeInterface, *interfaces.PipeInterface, func()) {
	t.Helper()
	pipeA := interfaces.NewPipeInterface("initiator", func(data []byte, iface interfaces.Interface) {
		tsA.Inbound(data, iface)
	})
	pipeB := interfaces.NewPipeInterface("receiver", func(data []byte, iface interfaces.Interface) {
		tsB.Inbound(data, iface)
	})
	pipeA.SetOther(pipeB)
	pipeB.SetOther(pipeA)
	cleanup := func() {
		_ = pipeA.Detach()
		_ = pipeB.Detach()
	}
	return pipeA, pipeB, cleanup
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile config error: %v", err)
	}
}

func TestNewReticulumEnablesBlackholeUpdaterWhenSourcesConfigured(t *testing.T) {
	t.Parallel()

	configDir := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, configDir, `[reticulum]
share_instance = No
blackhole_sources = [00112233445566778899aabbccddeeff]

[logging]
loglevel = 4
`)

	ts := NewTransportSystem(nil)
	r, err := NewReticulum(ts, configDir)
	if err != nil {
		t.Fatalf("NewReticulum() error = %v", err)
	}
	defer closeReticulum(t, r)

	if got := ts.EnableBlackholeUpdaterCallCount(); got != 1 {
		t.Fatalf("EnableBlackholeUpdaterCallCount() = %v, want 1", got)
	}
}

// TestNewReticulumParsesLogTimestampsConfig verifies applyConfig honors the
// [logging] logtimestamps setting (RNS/Reticulum.py:463-465, v1.3.2): the key
// is parsed as a bool and applied to the logger; when absent the default
// (true) is preserved.
func TestNewReticulumParsesLogTimestampsConfig(t *testing.T) {
	t.Parallel()

	// Explicitly disabled.
	cfgOff := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfgOff, `[reticulum]
share_instance = No

[logging]
loglevel = 4
logtimestamps = false
`)
	tsOff := NewTransportSystem(nil)
	rOff, err := NewReticulum(tsOff, cfgOff)
	if err != nil {
		t.Fatalf("NewReticulum() error = %v", err)
	}
	defer closeReticulum(t, rOff)
	if rOff.logger.GetLogTimestamps() {
		t.Fatalf("logtimestamps=false: GetLogTimestamps() = true, want false")
	}

	// Absent key → default true.
	cfgDefault := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfgDefault, `[reticulum]
share_instance = No

[logging]
loglevel = 4
`)
	tsDef := NewTransportSystem(nil)
	rDef, err := NewReticulum(tsDef, cfgDefault)
	if err != nil {
		t.Fatalf("NewReticulum() error = %v", err)
	}
	defer closeReticulum(t, rDef)
	if !rDef.logger.GetLogTimestamps() {
		t.Fatalf("logtimestamps absent: GetLogTimestamps() = false, want true (default)")
	}

	// Explicitly enabled.
	cfgOn := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfgOn, `[reticulum]
share_instance = No

[logging]
loglevel = 4
logtimestamps = true
`)
	tsOn := NewTransportSystem(nil)
	rOn, err := NewReticulum(tsOn, cfgOn)
	if err != nil {
		t.Fatalf("NewReticulum() error = %v", err)
	}
	defer closeReticulum(t, rOn)
	if !rOn.logger.GetLogTimestamps() {
		t.Fatalf("logtimestamps=true: GetLogTimestamps() = false, want true")
	}
}

// TestNewReticulumParsesBlackholeUpdateInterval verifies applyConfig honors
// [reticulum] blackhole_update_interval (RNS/Reticulum.py:601-604, v1.3.2):
// the float-minutes value is clamped to ≥2 and propagated to the
// TransportSystem and the running BlackholeUpdater as v*60 seconds.
func TestNewReticulumParsesBlackholeUpdateInterval(t *testing.T) {
	t.Parallel()

	// 2 minutes (the clamp floor) → 120s, and the updater should use it.
	cfg := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfg, `[reticulum]
share_instance = No
blackhole_sources = [00112233445566778899aabbccddeeff]
blackhole_update_interval = 2

[logging]
loglevel = 4
`)
	ts := NewTransportSystem(nil)
	r, err := NewReticulum(ts, cfg)
	if err != nil {
		t.Fatalf("NewReticulum() error = %v", err)
	}
	defer closeReticulum(t, r)

	const want = 2 * time.Minute
	if got := ts.BlackholeUpdateInterval(); got != want {
		t.Fatalf("TransportSystem.BlackholeUpdateInterval() = %v, want %v", got, want)
	}
	updater := ts.BlackholeUpdater()
	if updater == nil {
		t.Fatal("BlackholeUpdater() = nil, want a running updater")
	}
	if got := updater.UpdateInterval(); got != want {
		t.Fatalf("updater.UpdateInterval() = %v, want %v", got, want)
	}

	// Sub-2 values clamp to 2 minutes.
	cfgClamp := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfgClamp, `[reticulum]
share_instance = No
blackhole_sources = [00112233445566778899aabbccddeeff]
blackhole_update_interval = 0.5

[logging]
loglevel = 4
`)
	ts2 := NewTransportSystem(nil)
	r2, err := NewReticulum(ts2, cfgClamp)
	if err != nil {
		t.Fatalf("NewReticulum() clamp error = %v", err)
	}
	defer closeReticulum(t, r2)
	if got := ts2.BlackholeUpdateInterval(); got != want {
		t.Fatalf("clamped TransportSystem.BlackholeUpdateInterval() = %v, want %v (floor 2m)", got, want)
	}

	// Fractional ≥2 value → exact seconds (e.g. 2.5 min = 150s).
	cfgFrac := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfgFrac, `[reticulum]
share_instance = No
blackhole_sources = [00112233445566778899aabbccddeeff]
blackhole_update_interval = 2.5

[logging]
loglevel = 4
`)
	ts3 := NewTransportSystem(nil)
	r3, err := NewReticulum(ts3, cfgFrac)
	if err != nil {
		t.Fatalf("NewReticulum() frac error = %v", err)
	}
	defer closeReticulum(t, r3)
	if got := ts3.BlackholeUpdateInterval(); got != 150*time.Second {
		t.Fatalf("fractional TransportSystem.BlackholeUpdateInterval() = %v, want 150s", got)
	}
}

// TestNewReticulumHonorsPresetLogfilePath verifies the v1.2.0 logfile
// resolution (RNS/Reticulum.py:239-240): RNS.logfile = RNS.logfile or
// configdir+"/logfile". A pre-set logfile path is honored; when file logging
// is selected but no path is pre-set, New derives <configdir>/logfile.
func TestNewReticulumHonorsPresetLogfilePath(t *testing.T) {
	t.Parallel()

	// Case 1: caller pre-sets a custom logfile path → New honors it and does
	// NOT derive <configdir>/logfile.
	cfgDir := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfgDir, `[reticulum]
share_instance = No

[logging]
loglevel = 4
`)
	customDir := testutils.TempDir(t, "rns-preset-log-")
	customLog := filepath.Join(customDir, "custom.log")

	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)
	logger.SetLogDest(LogDestFile)
	logger.SetLogFilePath(customLog)

	ts := NewTransportSystem(nil)
	r, err := NewReticulumWithLogger(ts, cfgDir, logger)
	if err != nil {
		t.Fatalf("NewReticulumWithLogger() error = %v", err)
	}
	r.Logger().Notice("preset-logfile-marker")
	if !r.Logger().Flush() {
		t.Fatal("Logger().Flush() timed out waiting for the preset logfile write")
	}
	if err := r.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Errorf("Close: %v", err)
	}

	data, err := os.ReadFile(customLog)
	if err != nil {
		t.Fatalf("custom logfile %q not readable: %v", customLog, err)
	}
	if !strings.Contains(string(data), "preset-logfile-marker") {
		t.Fatalf("custom logfile %q = %q, want marker %q", customLog, string(data), "preset-logfile-marker")
	}
	// The derived default path must NOT have been written.
	if _, err := os.Stat(filepath.Join(cfgDir, "logfile")); err == nil {
		t.Fatalf("derived <configdir>/logfile unexpectedly created; pre-set path should have been honored")
	}

	// Case 2: file logging selected, no path pre-set → New derives <configdir>/logfile.
	cfgDir2 := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfgDir2, `[reticulum]
share_instance = No

[logging]
loglevel = 4
`)
	logger2 := NewLogger()
	logger2.SetLogLevel(LogExtreme)
	logger2.SetLogDest(LogDestFile)
	// Deliberately do NOT call SetLogFilePath.

	ts2 := NewTransportSystem(nil)
	r2, err := NewReticulumWithLogger(ts2, cfgDir2, logger2)
	if err != nil {
		t.Fatalf("NewReticulumWithLogger() derive error = %v", err)
	}
	r2.Logger().Notice("derived-logfile-marker")
	if !r2.Logger().Flush() {
		t.Fatal("Logger().Flush() timed out waiting for the derived logfile write")
	}
	if err := r2.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Errorf("Close: %v", err)
	}

	derivedLog := filepath.Join(cfgDir2, "logfile")
	data2, err := os.ReadFile(derivedLog)
	if err != nil {
		t.Fatalf("derived logfile %q not readable: %v", derivedLog, err)
	}
	if !strings.Contains(string(data2), "derived-logfile-marker") {
		t.Fatalf("derived logfile %q = %q, want marker %q", derivedLog, string(data2), "derived-logfile-marker")
	}
}

// TestNewReticulumErrorsOnSharedInstanceConflict verifies the v1.3.4
// shared-instance config conflict checks: New returns an error when
// share_instance + require_shared_instance are both enabled, or when
// shared_instance_type is set to an unrecognized value (which cannot be
// mapped to a use_af_unix decision). Mirrors RNS/Reticulum.py:403-405,446
// (require_shared abort) and the shared_instance_type ∈ {tcp,unix} guard at
// Reticulum.py:480-484.
func TestNewReticulumErrorsOnSharedInstanceConflict(t *testing.T) {
	t.Parallel()

	// Conflict A: share_instance + require_shared_instance both set.
	cfgA := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfgA, `[reticulum]
share_instance = Yes
require_shared_instance = Yes

[logging]
loglevel = 4
`)
	tsA := NewTransportSystem(nil)
	rA, errA := NewReticulum(tsA, cfgA)
	if errA == nil {
		closeReticulum(t, rA)
		t.Fatal("Conflict A: NewReticulum() = nil error, want error for share_instance + require_shared_instance")
	}
	if !strings.Contains(errA.Error(), "share_instance") && !strings.Contains(errA.Error(), "require_shared") {
		t.Fatalf("Conflict A error %q does not mention share_instance/require_shared", errA.Error())
	}

	// Conflict B: shared_instance_type set to an unrecognized value.
	cfgB := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfgB, `[reticulum]
share_instance = Yes
shared_instance_type = quic

[logging]
loglevel = 4
`)
	tsB := NewTransportSystem(nil)
	rB, errB := NewReticulum(tsB, cfgB)
	if errB == nil {
		closeReticulum(t, rB)
		t.Fatal("Conflict B: NewReticulum() = nil error, want error for unrecognized shared_instance_type")
	}
	if !strings.Contains(errB.Error(), "shared_instance_type") {
		t.Fatalf("Conflict B error %q does not mention shared_instance_type", errB.Error())
	}

	// Sanity: a recognized type (tcp) with share_instance and no
	// require_shared_instance does NOT error.
	cfgOK := testutils.TempDir(t, tempDirPrefix)
	port := reserveTCPPort(t)
	writeConfig(t, cfgOK, fmt.Sprintf(`[reticulum]
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v

[logging]
loglevel = 4

[interfaces]
`, port, reserveTCPPort(t)))
	tsOK := NewTransportSystem(nil)
	rOK, errOK := NewReticulum(tsOK, cfgOK)
	if errOK != nil {
		t.Fatalf("sanity tcp config: NewReticulum() error = %v, want nil", errOK)
	}
	closeReticulum(t, rOK)
}

func TestNewReticulumSharedInstanceServerThenClient(t *testing.T) {
	t.Parallel()
	ts1 := NewTransportSystem(nil)
	ts2 := NewTransportSystem(nil)

	port := reserveTCPPort(t)
	controlPort := reserveTCPPort(t)

	configTemplate := `[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v

[logging]
loglevel = 4

[interfaces]
`

	cfg1 := testutils.TempDir(t, tempDirPrefix)
	cfg2 := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfg1, fmt.Sprintf(configTemplate, t.Name(), port, controlPort))
	writeConfig(t, cfg2, fmt.Sprintf(configTemplate, t.Name(), port, controlPort))

	r1, err := NewReticulum(ts1, cfg1)
	if err != nil {
		t.Fatalf("failed to create reticulum 1: %v", err)
	}
	defer closeReticulum(t, r1)
	if !r1.isSharedInstance || r1.isConnectedToSharedInstance || r1.isStandaloneInstance {
		t.Fatalf("first instance role mismatch: shared=%v connected=%v standalone=%v", r1.isSharedInstance, r1.isConnectedToSharedInstance, r1.isStandaloneInstance)
	}

	r2, err := NewReticulum(ts2, cfg2)
	if err != nil {
		t.Fatalf("failed to create reticulum 2: %v", err)
	}
	defer closeReticulum(t, r2)
	if r2.isSharedInstance || !r2.isConnectedToSharedInstance || r2.isStandaloneInstance {
		t.Fatalf("second instance role mismatch: shared=%v connected=%v standalone=%v", r2.isSharedInstance, r2.isConnectedToSharedInstance, r2.isStandaloneInstance)
	}
}

func TestNewReticulumShareInstanceNoStandalone(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	cfg := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfg, fmt.Sprintf(`[reticulum]
share_instance = No
instance_control_port = %v

[logging]
loglevel = 4

[interfaces]
`, reserveTCPPort(t)))

	r, err := NewReticulum(ts, cfg)
	if err != nil {
		t.Fatalf("failed to create reticulum: %v", err)
	}
	defer closeReticulum(t, r)
	if r.isSharedInstance || r.isConnectedToSharedInstance || !r.isStandaloneInstance {
		t.Fatalf("instance role mismatch: shared=%v connected=%v standalone=%v", r.isSharedInstance, r.isConnectedToSharedInstance, r.isStandaloneInstance)
	}
}

func TestNewReticulumSharedInstanceUnixServerThenClientSameConfigDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shared-instance transport is not used on windows")
	}

	t.Parallel()
	ts1 := NewTransportSystem(nil)
	ts2 := NewTransportSystem(nil)

	cfg := testutils.TempDir(t, tempDirPrefix)
	// Use a shorter name for the socket to avoid path length limits on macOS
	instanceName := "rns-test"

	writeConfig(t, cfg, fmt.Sprintf(`[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = unix

[logging]
loglevel = 4

[interfaces]
`, instanceName))

	r1, err := NewReticulum(ts1, cfg)
	if err != nil {
		t.Fatalf("failed to create reticulum 1: %v", err)
	}
	defer closeReticulum(t, r1)
	if !r1.isSharedInstance || r1.isConnectedToSharedInstance || r1.isStandaloneInstance {
		t.Fatalf("first instance role mismatch: shared=%v connected=%v standalone=%v", r1.isSharedInstance, r1.isConnectedToSharedInstance, r1.isStandaloneInstance)
	}

	r2, err := NewReticulum(ts2, cfg)
	if err != nil {
		t.Fatalf("failed to create reticulum 2: %v", err)
	}
	defer closeReticulum(t, r2)
	if r2.isSharedInstance || !r2.isConnectedToSharedInstance || r2.isStandaloneInstance {
		t.Fatalf("second instance role mismatch: shared=%v connected=%v standalone=%v", r2.isSharedInstance, r2.isConnectedToSharedInstance, r2.isStandaloneInstance)
	}

	if r2.sharedInstanceInterface == nil || r2.sharedInstanceInterface.Type() != "LocalInterface" {
		t.Fatalf("expected second instance to use LocalInterface shared-instance client")
	}
}

func TestParseBoolLike(t *testing.T) {
	t.Parallel()
	truthy := []string{"1", "true", "True", "yes", "Y", "on"}
	for _, v := range truthy {
		if !parseBoolLike(v) {
			t.Fatalf("parseBoolLike(%q) = false, want true", v)
		}
	}

	falsy := []string{"0", "false", "False", "no", "N", "off", "unexpected"}
	for _, v := range falsy {
		if parseBoolLike(v) {
			t.Fatalf("parseBoolLike(%q) = true, want false", v)
		}
	}
}

func TestReticulumBackgroundJobs(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, "rns-test-")

	r, err := NewReticulumWithLogger(ts, tmpDir, nil)
	if err != nil {
		t.Fatalf("NewReticulumWithLogger: %v", err)
	}

	// Stop the maintenance goroutine NewTransportSystem started so the
	// test can directly drive the periodic job loop.
	ts.Stop()

	// Re-set the storage path so PersistData has a target.
	ts.mu.Lock()
	ts.storagePath = tmpDir
	ts.mu.Unlock()

	// The job loop should run and call PersistData at least once.
	var ticks atomic.Uint32
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				if err := ts.PersistData(); err == nil {
					ticks.Add(1)
				}
			}
		}
	}()

	// Wait for at least 2 ticks.
	for ticks.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	close(stopCh)
	<-doneCh

	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestInterfaceManagement(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, tmpDir, "[reticulum]\nshare_instance = No\n")
	r, err := NewReticulumWithLogger(ts, tmpDir, nil)
	if err != nil {
		t.Fatalf("NewReticulumWithLogger: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// Halt and resume a non-existent interface should not error.
	if err := r.HaltInterface(nil); err != nil {
		t.Fatalf("HaltInterface(nil): %v", err)
	}
	if err := r.ResumeInterface(nil); err != nil {
		t.Fatalf("ResumeInterface(nil): %v", err)
	}
	// Reload a non-existent interface should not error.
	if err := r.ReloadInterface(nil); err != nil {
		t.Fatalf("ReloadInterface(nil): %v", err)
	}
}
