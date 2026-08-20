// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// goldenIdentityHash is the hex identity hash for the golden key.
const goldenIdentityHash = "38d9dae25b98c254de7ae79e3ad0c108"

// goldenArmoredSig is the exact armored SSHSIG produced by Python
// `rngcs -Y sign -f id msg.txt` for the golden key and message. It is used
// only as a known-good fixture for the find-principals and check-novalidate
// behavior tests below; live sign byte-parity is covered by
// gorngcs-int_test.go TestGorngcsByteParity, which execs `rngcs -Y sign`.
const goldenArmoredSig = "-----BEGIN SSH SIGNATURE-----\n" +
	"U1NIU0lHAAAAAQAAADMAAAALc3NoLWVkMjU1MTkAAAAg9hxP+iApr6vcbY/TPWPJLYGw1Y\n" +
	"wI7htf9BGr6EBcIKYAAAADZ2l0AAAAAAAAAAZzaGEyNTYAAADgiZxLK74w13mThimeSNMw\n" +
	"f1bGYn3C9wlsUEc9kewsL/7aPQBV1fTmYWlKPaB7HrD8ktmpis4Mkt6SRD+dnBLEBYOoaG\n" +
	"FzaHR5cGWmc2hhMjU2pGhhc2jEIMCHnS+v6zWGkLyrovTTM0lidmq/mFMyndrcR/Be/Sy1\n" +
	"pG1ldGGCpnNpZ25lcsQQONna4luYwlTeeueeOtDBCKZwdWJrZXnEQODO+3jIlvHhcJBtti\n" +
	"aZW4qHkNgMx4cPVR+rFwJVx7Ik9hxP+iApr6vcbY/TPWPJLYGw1YwI7htf9BGr6EBcIKY=\n" +
	"-----END SSH SIGNATURE-----\n"

// TestSignVerifyRoundTrip signs a message with a fresh identity and verifies
// it in-process through the verify operation (the author field is set to the
// signer hash, as git commit objects require).
func TestSignVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	dir := testutils.TempDir(t, "gorngcs-roundtrip-")
	id, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	keyPath := filepath.Join(dir, "id")
	if err := id.ToFile(keyPath); err != nil {
		t.Fatalf("ToFile: %v", err)
	}

	// Build a fake commit object whose author email is the identity hash.
	commit := "tree abcdef\nauthor Name <" + id.HexHash + "> 0 +0000\ncommitter Name <" + id.HexHash + "> 0 +0000\n\nmessage\n"
	msgPath := filepath.Join(dir, "commit.txt")
	if err := os.WriteFile(msgPath, []byte(commit), 0o644); err != nil {
		t.Fatalf("write commit: %v", err)
	}

	var sErr bytes.Buffer
	code := run([]string{"-Y", "sign", "-f", keyPath, msgPath}, strings.NewReader(""), &discardWriter{}, &sErr)
	if code != 0 {
		t.Fatalf("sign exit %d, stderr=%s", code, sErr.String())
	}

	var vOut, vErr bytes.Buffer
	code = run([]string{"-Y", "verify", "-s", msgPath + ".sig"}, strings.NewReader(commit), &vOut, &vErr)
	if code != 0 {
		t.Fatalf("verify exit %d, stdout=%s stderr=%s", code, vOut.String(), vErr.String())
	}
	if !strings.Contains(vOut.String(), `Good "git" signature`) {
		t.Errorf("verify stdout missing good-signature line: %q", vOut.String())
	}
	if !strings.Contains(vOut.String(), id.HexHash) {
		t.Errorf("verify stdout missing signer hash %s: %q", id.HexHash, vOut.String())
	}
}

// TestVerifyWrongAuthor signs a commit whose author field claims a
// different identity than the signer. The RSG is valid (it signs the exact
// commit bytes), but the author email does not match the signer hash.
func TestVerifyWrongAuthor(t *testing.T) {
	t.Parallel()
	dir := testutils.TempDir(t, "gorngcs-wrongauthor-")
	id, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	keyPath := filepath.Join(dir, "id")
	if err := id.ToFile(keyPath); err != nil {
		t.Fatalf("ToFile: %v", err)
	}
	// Author claims a different hash than the signer.
	commit := "tree abc\nauthor Name <deadbeef> 0 +0000\ncommitter Name <deadbeef> 0 +0000\n\nm\n"
	msgPath := filepath.Join(dir, "commit.txt")
	if err := os.WriteFile(msgPath, []byte(commit), 0o644); err != nil {
		t.Fatalf("write commit: %v", err)
	}
	var sErr bytes.Buffer
	if code := run([]string{"-Y", "sign", "-f", keyPath, msgPath}, strings.NewReader(""), &discardWriter{}, &sErr); code != 0 {
		t.Fatalf("sign exit %d", code)
	}

	var vOut, vErr bytes.Buffer
	code := run([]string{"-Y", "verify", "-s", msgPath + ".sig"}, strings.NewReader(commit), &vOut, &vErr)
	if code != 1 {
		t.Fatalf("verify exit %d, want 1 (author mismatch)", code)
	}
	if !strings.Contains(vOut.String(), "Commit not signed by author") {
		t.Errorf("expected author-mismatch message on stdout, got: %q", vOut.String())
	}
}

// TestVerifyPrincipalMismatch verifies with a -I principal that does not
// match the signer hash.
func TestVerifyPrincipalMismatch(t *testing.T) {
	t.Parallel()
	dir := testutils.TempDir(t, "gorngcs-principal-")
	id, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	keyPath := filepath.Join(dir, "id")
	if err := id.ToFile(keyPath); err != nil {
		t.Fatalf("ToFile: %v", err)
	}
	commit := "tree abc\nauthor Name <" + id.HexHash + "> 0 +0000\ncommitter Name <" + id.HexHash + "> 0 +0000\n\nm\n"
	msgPath := filepath.Join(dir, "commit.txt")
	if err := os.WriteFile(msgPath, []byte(commit), 0o644); err != nil {
		t.Fatalf("write commit: %v", err)
	}
	var sErr bytes.Buffer
	if code := run([]string{"-Y", "sign", "-f", keyPath, msgPath}, strings.NewReader(""), &discardWriter{}, &sErr); code != 0 {
		t.Fatalf("sign exit %d", code)
	}
	var vOut, vErr bytes.Buffer
	code := run([]string{"-Y", "verify", "-I", "00ff00ff", "-s", msgPath + ".sig"}, strings.NewReader(commit), &vOut, &vErr)
	if code != 1 {
		t.Fatalf("verify exit %d, want 1 (principal mismatch)", code)
	}
	if !strings.Contains(vErr.String(), "Principal mismatch") {
		t.Errorf("expected principal mismatch on stderr, got: %q", vErr.String())
	}
}

// TestFindPrincipals parses a golden signature and prints the signer hash.
func TestFindPrincipals(t *testing.T) {
	t.Parallel()
	dir := testutils.TempDir(t, "gorngcs-find-")
	sigPath := filepath.Join(dir, "msg.txt.sig")
	if err := os.WriteFile(sigPath, []byte(goldenArmoredSig), 0o644); err != nil {
		t.Fatalf("write sig: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"-Y", "find-principals", "-s", sigPath}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("find-principals exit %d, stderr=%s", code, stderr.String())
	}
	got := strings.TrimSpace(stdout.String())
	if got != goldenIdentityHash {
		t.Errorf("find-principals = %q, want %q", got, goldenIdentityHash)
	}
}

// TestCheckNoValidate checks a golden signature structurally.
func TestCheckNoValidate(t *testing.T) {
	t.Parallel()
	dir := testutils.TempDir(t, "gorngcs-noval-")
	sigPath := filepath.Join(dir, "msg.txt.sig")
	if err := os.WriteFile(sigPath, []byte(goldenArmoredSig), 0o644); err != nil {
		t.Fatalf("write sig: %v", err)
	}
	var stderr bytes.Buffer
	code := run([]string{"-Y", "check-novalidate", "-s", sigPath}, strings.NewReader(""), &discardWriter{}, &stderr)
	if code != 0 {
		t.Fatalf("check-novalidate exit %d, want 0", code)
	}
}

// TestCheckNoValidateBadSig returns non-zero for a malformed signature.
func TestCheckNoValidateBadSig(t *testing.T) {
	t.Parallel()
	dir := testutils.TempDir(t, "gorngcs-novalbad-")
	sigPath := filepath.Join(dir, "bad.sig")
	if err := os.WriteFile(sigPath, []byte("not a signature"), 0o644); err != nil {
		t.Fatalf("write sig: %v", err)
	}
	var stderr bytes.Buffer
	code := run([]string{"-Y", "check-novalidate", "-s", sigPath}, strings.NewReader(""), &discardWriter{}, &stderr)
	if code == 0 {
		t.Fatal("check-novalidate on bad sig returned 0, want non-zero")
	}
}

// TestFindPrincipalsMissingSig returns non-zero when the sigfile is absent.
func TestFindPrincipalsMissingSig(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"-Y", "find-principals", "-s", "/nonexistent/sig"}, strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatal("find-principals missing sig returned 0, want non-zero")
	}
}

// TestParseArgs exercises argument parsing.
func TestParseArgs(t *testing.T) {
	t.Parallel()
	t.Run("sign", func(t *testing.T) {
		a, err := parseArgs([]string{"-Y", "sign", "-f", "key", "file"})
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if a.op != "sign" || a.keyfile != "key" || a.file != "file" {
			t.Errorf("parsed %+v", a)
		}
		if a.namespace != "git" {
			t.Errorf("namespace=%q want git", a.namespace)
		}
	})
	t.Run("O ignored", func(t *testing.T) {
		a, err := parseArgs([]string{"-Y", "sign", "-f", "key", "-O", "foo", "-Obar", "file"})
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if len(a.sshOptions) != 2 {
			t.Errorf("sshOptions=%v want 2", a.sshOptions)
		}
		if a.file != "file" {
			t.Errorf("file=%q", a.file)
		}
	})
	t.Run("missing op", func(t *testing.T) {
		if _, err := parseArgs([]string{"-f", "key"}); err == nil {
			t.Fatal("missing -Y should error")
		}
	})
	t.Run("bad op", func(t *testing.T) {
		if _, err := parseArgs([]string{"-Y", "bogus"}); err == nil {
			t.Fatal("bad op should error")
		}
	})
	t.Run("unknown arg", func(t *testing.T) {
		if _, err := parseArgs([]string{"-Y", "sign", "-Z"}); err == nil {
			t.Fatal("unknown arg should error")
		}
	})
}

// TestExtractCommitAuthor exercises the commit header parsing.
func TestExtractCommitAuthor(t *testing.T) {
	t.Parallel()
	commit := []byte("tree abc\nauthor Alice <alice@example.com> 0 +0000\ncommitter Bob <bob@example.com> 0 +0000\n\nbody\n")
	if got := extractCommitAuthor(commit); got != "alice@example.com" {
		t.Errorf("author=%q want alice@example.com", got)
	}
	if got := extractCommitCommitter(commit); got != "bob@example.com" {
		t.Errorf("committer=%q want bob@example.com", got)
	}
}

// TestExtractCommitTagger exercises tag object header parsing.
func TestExtractCommitTagger(t *testing.T) {
	t.Parallel()
	tag := []byte("object abc\ntype commit\ntag v1.0\ntagger Carol <carol@example.com> 0 +0000\n\nmessage\n")
	tagger, isTag := extractCommitTagger(tag)
	if !isTag {
		t.Error("isTag=false, want true")
	}
	if tagger != "carol@example.com" {
		t.Errorf("tagger=%q want carol@example.com", tagger)
	}
}

// TestExtractAuthorEmptyOnBlankOnly ensures an object with no author header
// returns empty.
func TestExtractAuthorEmptyOnBlankOnly(t *testing.T) {
	t.Parallel()
	if got := extractCommitAuthor([]byte("tree abc\n\nbody\n")); got != "" {
		t.Errorf("author=%q want empty", got)
	}
}

// discardWriter is an io.Writer that discards all output.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
