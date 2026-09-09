package testutils

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// StartWithReaper starts cmd and arms a detached watchdog that SIGKILLs the
// child if this test binary dies while the child is still running. A go test
// timeout (panic + os.Exit), Ctrl-C, or a crash skips every t.Cleanup and
// defer, which previously orphaned long-running test children (the rrc
// mini-hub's Python processes, reparented to init and running forever).
//
// macOS has no Pdeathsig, so the watchdog is a detached /bin/sh process that
// polls this process's PID once a second. It exits on its own once either
// this process or the child disappears, so it needs no reaping and never
// outlives the test run.
//
// The kill is fingerprinted: the child's `ps -o lstart=` start time is
// recorded at spawn, and the watchdog only signals the child's PID if that
// PID still reports the same start time — so a recycled PID is never
// signalled.
func StartWithReaper(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	armReaper(os.Getpid(), cmd.Process.Pid, psLstart(cmd.Process.Pid))
	return nil
}

// armReaper spawns the watchdog for a child owned by the process at testPid.
// Separated from StartWithReaper so the self-test can exercise it: a test
// process cannot kill itself to prove the watchdog fires, but it can stand
// in a decoy process for testPid.
func armReaper(testPid, childPid int, fingerprint string) {
	if fingerprint == "" {
		// No fingerprint (ps unavailable): cannot kill safely, skip.
		return
	}
	test, me := strconv.Itoa(testPid), strconv.Itoa(childPid)
	script := "while kill -0 " + test + " 2>/dev/null && " +
		"kill -0 " + me + " 2>/dev/null; do sleep 1; done; " +
		"if ! kill -0 " + test + " 2>/dev/null && " +
		"[ \"$(ps -o lstart= -p " + me + " 2>/dev/null | xargs)\" = \"" +
		fingerprint + "\" ]; then " +
		"kill -9 " + me + " 2>/dev/null; fi"
	// Deliberately not waited on: this process's death is the watcher's
	// exit signal. If the child exits first (the normal path), the watcher
	// still exits when this process does.
	_ = exec.Command("/bin/sh", "-c", script).Start()
}

func psLstart(pid int) string {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	// Normalize all whitespace runs (ps pads with trailing spaces; the
	// watcher's shell side normalizes with xargs the same way).
	return strings.Join(strings.Fields(string(out)), " ")
}
