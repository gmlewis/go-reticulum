// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

func tempDir(t *testing.T) (string, func()) {
	t.Helper()
	baseDir := ""
	if runtime.GOOS == "darwin" {
		baseDir = "/tmp"
	}
	dir, err := os.MkdirTemp(baseDir, "gornsh-test-")
	if err != nil {
		t.Fatalf("tempDir error: %v", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(dir)
	}
	return dir, cleanup
}

func TestProtocolPayloadParityWithPython(t *testing.T) {
	t.Parallel()

	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}

	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	payloadPath := filepath.Join(tmpDir, "payload.msgpack")

	executePayload, err := msgpack.Pack([]any{[]any{"/bin/sh", "-lc", "echo hi"}, true, false, true, nil, "xterm", 24, 80, nil, nil})
	if err != nil {
		t.Fatalf("pack execute payload: %v", err)
	}
	if err := os.WriteFile(payloadPath, executePayload, 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	checkPy := `import sys
from RNS.vendor import umsgpack
with open(sys.argv[1], 'rb') as f:
    data = umsgpack.unpackb(f.read())
if not isinstance(data, (list, tuple)) or len(data) != 10:
    raise SystemExit(2)
if data[0][0] != '/bin/sh' or data[1] is not True or data[2] is not False or data[3] is not True:
    raise SystemExit(3)
print('ok')
`
	scriptPath := filepath.Join(tmpDir, "check.py")
	if err := os.WriteFile(scriptPath, []byte(checkPy), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cmd := exec.Command(pythonPath, scriptPath, payloadPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getRnshPythonPath())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python parity check failed: %v\n%v", err, string(output))
	}
}

func TestErrorExitOrderingPayloadParityWithPython(t *testing.T) {
	t.Parallel()

	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}

	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	payloadPath := filepath.Join(tmpDir, "ordered.msgpack")

	warnPayload, err := (&errorMessage{Message: "temporary issue", Fatal: false, Data: nil}).pack()
	if err != nil {
		t.Fatalf("pack warning payload: %v", err)
	}
	exitPayload, err := (&commandExitedMessage{ReturnCode: 7}).pack()
	if err != nil {
		t.Fatalf("pack exit payload: %v", err)
	}
	fatalPayload, err := (&errorMessage{Message: "fatal issue", Fatal: true, Data: nil}).pack()
	if err != nil {
		t.Fatalf("pack fatal payload: %v", err)
	}

	sequence, err := msgpack.Pack([]any{
		[]any{makeMsgType(msgTypeError), warnPayload},
		[]any{makeMsgType(msgTypeCommandExited), exitPayload},
		[]any{makeMsgType(msgTypeError), fatalPayload},
	})
	if err != nil {
		t.Fatalf("pack ordered sequence: %v", err)
	}
	if err := os.WriteFile(payloadPath, sequence, 0o644); err != nil {
		t.Fatalf("write ordered payload: %v", err)
	}

	checkPy := `import sys
from RNS.vendor import umsgpack

MSG_ERROR = (0xac << 8) | 6
MSG_EXIT = (0xac << 8) | 7

with open(sys.argv[1], 'rb') as f:
    seq = umsgpack.unpackb(f.read())

if not isinstance(seq, (list, tuple)) or len(seq) != 3:
    raise SystemExit(2)

def decode_frame(frame):
    if not isinstance(frame, (list, tuple)) or len(frame) != 2:
        raise SystemExit(3)
    msg_type = frame[0]
    payload = umsgpack.unpackb(frame[1])
    return msg_type, payload

t0, p0 = decode_frame(seq[0])
t1, p1 = decode_frame(seq[1])
t2, p2 = decode_frame(seq[2])

if t0 != MSG_ERROR or p0[0] != 'temporary issue' or p0[1] is not False:
    raise SystemExit(4)
if t1 != MSG_EXIT or p1 != 7:
    raise SystemExit(5)
if t2 != MSG_ERROR or p2[0] != 'fatal issue' or p2[1] is not True:
    raise SystemExit(6)
print('ok')
`
	scriptPath := filepath.Join(tmpDir, "check_ordered.py")
	if err := os.WriteFile(scriptPath, []byte(checkPy), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cmd := exec.Command(pythonPath, scriptPath, payloadPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getRnshPythonPath())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python parity check failed: %v\n%v", err, string(output))
	}
}

func TestRetryMetadataAndExitPayloadParityWithPython(t *testing.T) {
	t.Parallel()

	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}

	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	payloadPath := filepath.Join(tmpDir, "retry_ordered.msgpack")

	retryPayload, err := (&errorMessage{
		Message: "retrying command send",
		Fatal:   false,
		Data: map[string]any{
			"retry":      true,
			"attempt":    3,
			"backoff_ms": 200,
		},
	}).pack()
	if err != nil {
		t.Fatalf("pack retry payload: %v", err)
	}
	exitPayload, err := (&commandExitedMessage{ReturnCode: 0}).pack()
	if err != nil {
		t.Fatalf("pack exit payload: %v", err)
	}

	sequence, err := msgpack.Pack([]any{
		[]any{makeMsgType(msgTypeError), retryPayload},
		[]any{makeMsgType(msgTypeCommandExited), exitPayload},
	})
	if err != nil {
		t.Fatalf("pack retry ordered sequence: %v", err)
	}
	if err := os.WriteFile(payloadPath, sequence, 0o644); err != nil {
		t.Fatalf("write retry ordered payload: %v", err)
	}

	checkPy := `import sys
from RNS.vendor import umsgpack

MSG_ERROR = (0xac << 8) | 6
MSG_EXIT = (0xac << 8) | 7

with open(sys.argv[1], 'rb') as f:
    seq = umsgpack.unpackb(f.read())

if not isinstance(seq, (list, tuple)) or len(seq) != 2:
    raise SystemExit(2)

def decode_frame(frame):
    if not isinstance(frame, (list, tuple)) or len(frame) != 2:
        raise SystemExit(3)
    msg_type = frame[0]
    payload = umsgpack.unpackb(frame[1])
    return msg_type, payload

t0, p0 = decode_frame(seq[0])
t1, p1 = decode_frame(seq[1])

if t0 != MSG_ERROR:
    raise SystemExit(4)
if p0[0] != 'retrying command send' or p0[1] is not False:
    raise SystemExit(5)
if not isinstance(p0[2], dict):
    raise SystemExit(6)
if p0[2].get('retry') is not True or p0[2].get('attempt') != 3 or p0[2].get('backoff_ms') != 200:
    raise SystemExit(7)

if t1 != MSG_EXIT or p1 != 0:
    raise SystemExit(8)
print('ok')
`
	scriptPath := filepath.Join(tmpDir, "check_retry_ordered.py")
	if err := os.WriteFile(scriptPath, []byte(checkPy), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cmd := exec.Command(pythonPath, scriptPath, payloadPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getRnshPythonPath())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python parity check failed: %v\n%v", err, string(output))
	}
}

func getRnshPythonPath() string {
	rnshRoot := os.Getenv("ORIGINAL_RNSH_REPO_DIR")
	reticulumRoot := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR")
	if rnshRoot != "" && reticulumRoot != "" {
		return rnshRoot + string(os.PathListSeparator) + reticulumRoot
	}
	if rnshRoot == "" {
		log.Fatalf("missing required environment variable: ORIGINAL_RNSH_REPO_DIR")
	}
	if reticulumRoot == "" {
		log.Fatalf("missing required environment variable: ORIGINAL_RETICULUM_REPO_DIR")
	}
	return "" // unreachable
}
