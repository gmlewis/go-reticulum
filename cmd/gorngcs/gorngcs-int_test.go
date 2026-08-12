// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// findRngcs returns the path to the Python rngcs binary, or skips the test.
func findRngcs(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("rngcs"); err != nil {
		t.Skip("rngcs not found in PATH, skipping Go/Python parity test")
	}
	return ""
}

// findRnidForGcs returns the path to the Python rnid binary, or skips.
func findRnidForGcs(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("rnid"); err != nil {
		t.Skip("rnid not found in PATH, skipping Go/Python parity test")
	}
	return ""
}

// buildGorngcs builds the gorngcs binary into a temp dir and returns its
// path.
func buildGorngcs(t *testing.T) string {
	t.Helper()
	tmpDir := testutils.TempDir(t, "gorngcs-build-")
	bin := filepath.Join(tmpDir, "gorngcs")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build gorngcs: %v\n%v", err, string(out))
	}
	return bin
}

// genPythonIdentity generates a Reticulum identity with `rnid -g` into
// idPath and returns the hex identity hash reported by rnid.
func genPythonIdentity(t *testing.T, idPath string) string {
	t.Helper()
	out, err := exec.Command("rnid", "-g", idPath).CombinedOutput()
	if err != nil {
		t.Fatalf("rnid -g failed: %v\n%v", err, string(out))
	}
	// Output line: "New identity <hexhash> written to <path>".
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, "<"); i >= 0 {
			if j := strings.Index(line[i+1:], ">"); j >= 0 {
				return line[i+1 : i+1+j]
			}
		}
	}
	t.Fatalf("could not parse identity hash from rnid output: %s", string(out))
	return ""
}

// makeCommitObject builds a minimal git commit object whose author and
// committer emails are set to identityHash (as rngcs verify requires).
func makeCommitObject(identityHash string) string {
	return "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\n" +
		"author Test <" + identityHash + "> 1700000000 +0000\n" +
		"committer Test <" + identityHash + "> 1700000000 +0000\n" +
		"\nTest commit\n"
}

// TestGorngcsSignPythonVerify signs a commit object with gorngcs and
// verifies the resulting signature with Python rngcs.
func TestGorngcsSignPythonVerify(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	findRngcs(t)
	findRnidForGcs(t)
	gorngcsBin := buildGorngcs(t)
	dir := testutils.TempDir(t, "gorngcs-pyverify-")
	idPath := filepath.Join(dir, "id")
	idHash := genPythonIdentity(t, idPath)

	commit := makeCommitObject(idHash)
	commitPath := filepath.Join(dir, "commit.txt")
	if err := os.WriteFile(commitPath, []byte(commit), 0o644); err != nil {
		t.Fatalf("write commit: %v", err)
	}

	// Sign with gorngcs.
	if out, err := exec.Command(gorngcsBin, "-Y", "sign", "-f", idPath, commitPath).CombinedOutput(); err != nil {
		t.Fatalf("gorngcs sign failed: %v\n%v", err, string(out))
	}
	sigPath := commitPath + ".sig"
	if _, err := os.Stat(sigPath); err != nil {
		t.Fatalf("signature file not created: %v", err)
	}

	// Verify with Python rngcs. The commit object is piped to stdin.
	verifyCmd := exec.Command("rngcs", "-Y", "verify", "-s", sigPath)
	verifyCmd.Stdin = strings.NewReader(commit)
	out, err := verifyCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python rngcs verify failed: %v\n%v", err, string(out))
	}
	if !strings.Contains(string(out), `Good "git" signature`) {
		t.Errorf("Python verify output missing good-signature line: %q", string(out))
	}
}

// TestPythonSignGorngcsVerify signs a commit object with Python rngcs and
// verifies the resulting signature with gorngcs.
func TestPythonSignGorngcsVerify(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	findRngcs(t)
	findRnidForGcs(t)
	gorngcsBin := buildGorngcs(t)
	dir := testutils.TempDir(t, "gorngcs-goverify-")
	idPath := filepath.Join(dir, "id")
	idHash := genPythonIdentity(t, idPath)

	commit := makeCommitObject(idHash)
	commitPath := filepath.Join(dir, "commit.txt")
	if err := os.WriteFile(commitPath, []byte(commit), 0o644); err != nil {
		t.Fatalf("write commit: %v", err)
	}

	// Sign with Python rngcs.
	if out, err := exec.Command("rngcs", "-Y", "sign", "-f", idPath, commitPath).CombinedOutput(); err != nil {
		t.Fatalf("Python rngcs sign failed: %v\n%v", err, string(out))
	}
	sigPath := commitPath + ".sig"
	if _, err := os.Stat(sigPath); err != nil {
		t.Fatalf("signature file not created by Python: %v", err)
	}

	// Verify with gorngcs.
	verifyCmd := exec.Command(gorngcsBin, "-Y", "verify", "-s", sigPath)
	verifyCmd.Stdin = strings.NewReader(commit)
	out, err := verifyCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gorngcs verify failed: %v\n%v", err, string(out))
	}
	if !strings.Contains(string(out), `Good "git" signature`) {
		t.Errorf("gorngcs verify output missing good-signature line: %q", string(out))
	}
}

// TestGorngcsByteParity signs the same file with gorngcs and Python rngcs
// and asserts the armored signatures are byte-identical. Ed25519 signatures
// are deterministic and the RSG envelope is built in Python dict insertion
// order, so parity is achievable.
func TestGorngcsByteParity(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	findRngcs(t)
	findRnidForGcs(t)
	gorngcsBin := buildGorngcs(t)
	dir := testutils.TempDir(t, "gorngcs-parity-")
	idPath := filepath.Join(dir, "id")
	genPythonIdentity(t, idPath)

	// Use a plain file (not a commit object) for sign — rngcs sign signs
	// raw file bytes regardless of content.
	msg := "byte parity test payload\n"
	msgPath := filepath.Join(dir, "msg.txt")
	if err := os.WriteFile(msgPath, []byte(msg), 0o644); err != nil {
		t.Fatalf("write msg: %v", err)
	}

	// Sign with gorngcs into one dir, Python into another (to avoid the
	// shared msg.txt.sig clobbering).
	goDir := testutils.TempDir(t, "gorngcs-parity-go-")
	pyDir := testutils.TempDir(t, "gorngcs-parity-py-")
	goMsg := filepath.Join(goDir, "msg.txt")
	pyMsg := filepath.Join(pyDir, "msg.txt")
	if err := os.WriteFile(goMsg, []byte(msg), 0o644); err != nil {
		t.Fatalf("write go msg: %v", err)
	}
	if err := os.WriteFile(pyMsg, []byte(msg), 0o644); err != nil {
		t.Fatalf("write py msg: %v", err)
	}

	if out, err := exec.Command(gorngcsBin, "-Y", "sign", "-f", idPath, goMsg).CombinedOutput(); err != nil {
		t.Fatalf("gorngcs sign failed: %v\n%v", err, string(out))
	}
	if out, err := exec.Command("rngcs", "-Y", "sign", "-f", idPath, pyMsg).CombinedOutput(); err != nil {
		t.Fatalf("Python rngcs sign failed: %v\n%v", err, string(out))
	}

	goSig, err := os.ReadFile(goMsg + ".sig")
	if err != nil {
		t.Fatalf("read go sig: %v", err)
	}
	pySig, err := os.ReadFile(pyMsg + ".sig")
	if err != nil {
		t.Fatalf("read py sig: %v", err)
	}
	if string(goSig) != string(pySig) {
		t.Errorf("armored signatures differ:\n Go:   %q\n Python: %q", string(goSig), string(pySig))
	}
}

// TestGorngcsFindPrincipalsParity confirms gorngcs find-principals prints
// the same hash Python rngcs reports.
func TestGorngcsFindPrincipalsParity(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	findRngcs(t)
	findRnidForGcs(t)
	gorngcsBin := buildGorngcs(t)
	dir := testutils.TempDir(t, "gorngcs-findparity-")
	idPath := filepath.Join(dir, "id")
	idHash := genPythonIdentity(t, idPath)

	msg := "find principals parity\n"
	msgPath := filepath.Join(dir, "msg.txt")
	if err := os.WriteFile(msgPath, []byte(msg), 0o644); err != nil {
		t.Fatalf("write msg: %v", err)
	}
	if out, err := exec.Command("rngcs", "-Y", "sign", "-f", idPath, msgPath).CombinedOutput(); err != nil {
		t.Fatalf("Python rngcs sign failed: %v\n%v", err, string(out))
	}
	sigPath := msgPath + ".sig"

	goOut, err := exec.Command(gorngcsBin, "-Y", "find-principals", "-s", sigPath).CombinedOutput()
	if err != nil {
		t.Fatalf("gorngcs find-principals failed: %v\n%v", err, string(goOut))
	}
	pyOut, err := exec.Command("rngcs", "-Y", "find-principals", "-s", sigPath).CombinedOutput()
	if err != nil {
		t.Fatalf("Python rngcs find-principals failed: %v\n%v", err, string(pyOut))
	}
	goHash := strings.TrimSpace(string(goOut))
	pyHash := strings.TrimSpace(string(pyOut))
	if goHash != pyHash {
		t.Errorf("find-principals hash mismatch: Go=%q Python=%q", goHash, pyHash)
	}
	if goHash != idHash {
		t.Errorf("find-principals hash=%q want identity hash %q", goHash, idHash)
	}
}

// TestGorngcsCheckNoValidateParity confirms gorngcs check-novalidate and
// Python rngcs check-novalidate agree on a valid signature.
func TestGorngcsCheckNoValidateParity(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	findRngcs(t)
	findRnidForGcs(t)
	gorngcsBin := buildGorngcs(t)
	dir := testutils.TempDir(t, "gorngcs-noval-parity-")
	idPath := filepath.Join(dir, "id")
	genPythonIdentity(t, idPath)

	msg := "check novalidate parity\n"
	msgPath := filepath.Join(dir, "msg.txt")
	if err := os.WriteFile(msgPath, []byte(msg), 0o644); err != nil {
		t.Fatalf("write msg: %v", err)
	}
	if out, err := exec.Command("rngcs", "-Y", "sign", "-f", idPath, msgPath).CombinedOutput(); err != nil {
		t.Fatalf("Python rngcs sign failed: %v\n%v", err, string(out))
	}
	sigPath := msgPath + ".sig"

	goCmd := exec.Command(gorngcsBin, "-Y", "check-novalidate", "-s", sigPath)
	if out, err := goCmd.CombinedOutput(); err != nil {
		t.Fatalf("gorngcs check-novalidate failed (exit non-zero): %v\n%v", err, string(out))
	}
	pyCmd := exec.Command("rngcs", "-Y", "check-novalidate", "-s", sigPath)
	if out, err := pyCmd.CombinedOutput(); err != nil {
		t.Fatalf("Python rngcs check-novalidate failed (exit non-zero): %v\n%v", err, string(out))
	}
}
