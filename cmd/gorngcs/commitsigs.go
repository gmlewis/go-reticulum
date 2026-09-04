// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rsg"
)

// sigPubOffset is the byte offset of the Ed25519 signing public key within
// the concatenated identity public key (the second half).
const sigPubOffset = rns.IdentityKeySize / 8 / 2

// cliArgs holds the parsed command-line arguments, mirroring the argparse
// options of commitsigs.py.
type cliArgs struct {
	op         string
	version    bool
	namespace  string
	keyfile    string
	principal  string
	sigfile    string
	file       string
	sshOptions []string
}

// parseArgs parses the rngcs-style argument list. It mirrors
// commitsigs.py:main, including the ignored repeatable -O option and the
// positional file argument.
func parseArgs(argv []string) (*cliArgs, error) {
	a := &cliArgs{namespace: namespaceGit}
	i := 0
	for i < len(argv) {
		arg := argv[i]
		switch {
		case arg == "--version":
			a.version = true
			i++
		case arg == "-Y":
			if i+1 >= len(argv) {
				return nil, errors.New("-Y requires a value")
			}
			a.op = argv[i+1]
			i += 2
		case arg == "-n":
			if i+1 >= len(argv) {
				return nil, errors.New("-n requires a value")
			}
			a.namespace = argv[i+1]
			i += 2
		case arg == "-f":
			if i+1 >= len(argv) {
				return nil, errors.New("-f requires a value")
			}
			a.keyfile = argv[i+1]
			i += 2
		case arg == "-I":
			if i+1 >= len(argv) {
				return nil, errors.New("-I requires a value")
			}
			a.principal = argv[i+1]
			i += 2
		case arg == "-s":
			if i+1 >= len(argv) {
				return nil, errors.New("-s requires a value")
			}
			a.sigfile = argv[i+1]
			i += 2
		case arg == "-O":
			if i+1 >= len(argv) {
				return nil, errors.New("-O requires a value")
			}
			a.sshOptions = append(a.sshOptions, argv[i+1])
			i += 2
		case strings.HasPrefix(arg, "-O"):
			a.sshOptions = append(a.sshOptions, arg[2:])
			i++
		case strings.HasPrefix(arg, "-"):
			return nil, fmt.Errorf("Error: Unknown argument: %s", arg)
		default:
			if a.file != "" {
				return nil, fmt.Errorf("Error: Unknown argument: %s", arg)
			}
			a.file = arg
			i++
		}
	}
	if a.version {
		return a, nil
	}
	if a.op == "" {
		return nil, errors.New("-Y operation is required")
	}
	switch a.op {
	case "sign", "find-principals", "check-novalidate", "verify":
	default:
		return nil, fmt.Errorf("Error: Unknown operation: %s", a.op)
	}
	return a, nil
}

// pubkeyWireFormat returns the SSH public key wire format for an identity:
// ssh_string("ssh-ed25519") + ssh_string(sig_pub_bytes). It matches
// commitsigs.py:get_pubkey_wire_format.
func pubkeyWireFormat(id *rns.Identity) []byte {
	sigPub := id.GetPublicKey()[sigPubOffset:]
	return append(sshString([]byte(sshEd25519Name)), sshString(sigPub)...)
}

// sign performs the "sign" operation. It loads the identity from the
// keyfile, signs the file (or stdin) with an RSG, wraps it in an SSHSIG
// blob, armors it, and writes the result to <file>.sig (or stdout). It
// returns the process exit code.
func (a *cliArgs) sign(stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	keyfile := a.keyfile
	if keyfile == "" || !fileExists(keyfile) {
		_, _ = fmt.Fprintf(stderr, "Identity file not found: %s\n", keyfile)
		return 1
	}
	identity, err := rns.FromFile(keyfile, nil)
	if err != nil || identity == nil {
		_, _ = fmt.Fprintln(stderr, "Error: Could not load identity or identity has no private key")
		return 1
	}
	if identity.GetPrivateKey() == nil {
		_, _ = fmt.Fprintln(stderr, "Error: Could not load identity or identity has no private key")
		return 1
	}

	var message []byte
	sigFile := ""
	if a.file != "" && fileExists(a.file) {
		data, err := os.ReadFile(a.file)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error reading file: %v\n", err)
			return 1
		}
		message = data
		sigFile = a.file + ".sig"
	} else {
		data, err := io.ReadAll(stdin)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error reading stdin: %v\n", err)
			return 1
		}
		message = data
	}

	rsgBlob, err := rsg.Create(identity, message)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error creating signature: %v\n", err)
		return 1
	}

	pubKeyWire := pubkeyWireFormat(identity)
	sshSigBlob := createSSHSig(
		pubKeyWire,
		[]byte(namespaceGit),
		[]byte(reservedEmpty),
		[]byte(hashAlgorithm),
		rsgBlob,
	)
	armored := armorSSHSig(sshSigBlob)

	if sigFile != "" {
		if err := os.WriteFile(sigFile, []byte(armored), 0o644); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error writing signature file: %v\n", err)
			return 1
		}
	} else {
		_, _ = fmt.Fprint(stdout, armored)
	}
	return 0
}

// findPrincipals performs the "find-principals" operation. It reads the
// signature file, parses the SSHSIG blob, and prints the signer identity
// hash (hex, no delimiter) extracted from the RSG envelope.
func (a *cliArgs) findPrincipals(stdout io.Writer, stderr io.Writer) int {
	sigfile := a.sigfile
	if sigfile == "" || !fileExists(sigfile) {
		_, _ = fmt.Fprintln(stderr, "Error: Signature file not found")
		return 1
	}
	armored, err := os.ReadFile(sigfile)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error reading signature file: %v\n", err)
		return 1
	}
	parsed, err := parseArmoredSig(armored)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error parsing SSH signature: %v\n", err)
		return 1
	}
	if !bytes.Equal(parsed.Namespace, []byte(namespaceGit)) {
		_, _ = fmt.Fprintf(stderr, "Error: Namespace mismatch: %s\n", parsed.Namespace)
		return 1
	}
	extracted, err := rsg.ExtractSignedData(parsed.SignatureData)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Could not determine signer identity: %v\n", err)
		return 1
	}
	meta, _ := extracted["meta"].(map[any]any)
	if meta == nil {
		_, _ = fmt.Fprintln(stderr, "Could not determine signer identity: missing meta")
		return 1
	}
	signer, _ := meta["signer"].([]byte)
	if signer == nil {
		_, _ = fmt.Fprintln(stderr, "Could not determine signer identity: missing signer")
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%x\n", signer)
	return 0
}

// checkNoValidate performs the "check-novalidate" operation. It checks the
// signature structurally without validating against allowed signers.
func (a *cliArgs) checkNoValidate(stderr io.Writer) int {
	sigfile := a.sigfile
	if sigfile == "" || !fileExists(sigfile) {
		return 1
	}
	armored, err := os.ReadFile(sigfile)
	if err != nil {
		return 1
	}
	parsed, err := parseArmoredSig(armored)
	if err != nil {
		return 1
	}
	if !bytes.Equal(parsed.Namespace, []byte(namespaceGit)) {
		return 1
	}
	extracted, err := rsg.ExtractSignedData(parsed.SignatureData)
	if err != nil || extracted == nil {
		return 1
	}
	return 0
}

// verify performs the "verify" operation. It reads the signature file and
// the commit object from stdin, validates the RSG, and checks that the
// commit author (or tagger for tags) matches the signing identity hash.
// Status messages follow commitsigs.py: "Commit not signed by author" and
// the "Good signature" line go to stdout; "Invalid signature" and
// "Principal mismatch" go to stderr.
func (a *cliArgs) verify(stdin io.Reader, stdout, stderr io.Writer) int {
	sigfile := a.sigfile
	if sigfile == "" || !fileExists(sigfile) {
		_, _ = fmt.Fprintln(stderr, "Error: Signature file not found")
		return 1
	}
	message, err := io.ReadAll(stdin)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error reading stdin: %v\n", err)
		return 1
	}
	armored, err := os.ReadFile(sigfile)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error parsing signature: %v\n", err)
		return 1
	}
	parsed, err := parseArmoredSig(armored)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error parsing signature: %v\n", err)
		return 1
	}
	if !bytes.Equal(parsed.Namespace, []byte(namespaceGit)) {
		_, _ = fmt.Fprintln(stderr, "Invalid commit signature namespace")
		return 1
	}

	author := extractCommitAuthor(message)
	committer := extractCommitCommitter(message)
	tagger, isTag := extractCommitTagger(message)
	_ = committer

	signingID, vErr := rsg.Validate(parsed.SignatureData, message, nil)
	if vErr != nil {
		_, _ = fmt.Fprintln(stderr, "Invalid signature")
		return 1
	}

	if isTag {
		author = tagger
	}

	signerHash := signingID.HexHash
	if author != signerHash {
		_, _ = fmt.Fprintf(stdout, "Commit not signed by author <%s>\n", author)
		return 1
	}
	if a.principal != "" && a.principal != signerHash {
		_, _ = fmt.Fprintln(stderr, "Principal mismatch")
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Good \"git\" signature for commit, signed with Reticulum Identity key <%s>\n", signerHash)
	return 0
}

// parseArmoredSig reads an armored SSHSIG string and returns the parsed
// SSHSIG structure.
func parseArmoredSig(armored []byte) (*sshSig, error) {
	raw, err := unarmorSSHSig(string(armored))
	if err != nil {
		return nil, err
	}
	return parseSSHSig(raw)
}

// extractCommitAuthor returns the author email (the text between < and >)
// from the "author " header line of a git commit object. It matches
// commitsigs.py:extract_commit_author.
func extractCommitAuthor(message []byte) string {
	return extractAngleField(message, []byte("author "))
}

// extractCommitCommitter returns the committer email from the "committer "
// header line of a git commit object. It matches
// commitsigs.py:extract_commit_committer.
func extractCommitCommitter(message []byte) string {
	return extractAngleField(message, []byte("committer "))
}

// extractCommitTagger returns the tagger email and whether the object is a
// tag. It matches commitsigs.py:extract_commit_tagger.
func extractCommitTagger(message []byte) (tagger string, isTag bool) {
	const taggerTarget = "tagger "
	for line := range bytes.SplitSeq(message, []byte("\n")) {
		if len(line) == 0 {
			break
		}
		if bytes.HasPrefix(line, []byte("tag ")) {
			isTag = true
			continue
		}
		if isTag && bytes.HasPrefix(line, []byte(taggerTarget)) {
			spos := bytes.IndexByte(line, '<')
			epos := bytes.IndexByte(line, '>')
			if spos > len(taggerTarget) && epos > spos && epos < len(line)-1 {
				return string(line[spos+1 : epos]), true
			}
		}
	}
	return "", isTag
}

// extractAngleField scans the header lines of a git object (up to the first
// blank line) for one beginning with target, and returns the text between
// the first < and >. It mirrors the shared logic of extract_commit_author
// and extract_commit_committer.
func extractAngleField(message []byte, target []byte) string {
	for line := range bytes.SplitSeq(message, []byte("\n")) {
		if len(line) == 0 {
			break
		}
		if bytes.HasPrefix(line, target) {
			spos := bytes.IndexByte(line, '<')
			epos := bytes.IndexByte(line, '>')
			if spos > len(target) && epos > spos && epos < len(line)-1 {
				return string(line[spos+1 : epos])
			}
		}
	}
	return ""
}

// fileExists reports whether path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
