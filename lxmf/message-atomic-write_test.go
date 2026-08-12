// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// newAtomicWriteMessage builds a packed message whose packed container is
// large enough that a non-atomic write exposes a visible partial-write
// window to a concurrent reader.
func newAtomicWriteMessage(t *testing.T) *Message {
	t.Helper()
	ts := rns.NewTransportSystem(nil)
	destID := mustTestNewIdentity(t, true)
	srcID := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	src := mustTestNewDestination(t, ts, srcID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	// ~256 KiB content so a direct (non-rename) write is observably partial.
	msg := mustTestNewMessage(t, dest, src, string(bytes.Repeat([]byte("LXMF-atomic-write-payload-"), 10_000)), "title", nil)
	mustTest(t, msg.Pack())
	return msg
}

// TestWriteToDirectoryLeavesNoTmpRemnant covers Phase 16 task 1: after a
// successful WriteToDirectory the destination contains the final file and no
// leftover ".tmp.*" temporary file, mirroring Python's tmp+rename+cleanup in
// LXMessage.write_to_directory (LXMessage.py:677-693, v0.9.9).
func TestWriteToDirectoryLeavesNoTmpRemnant(t *testing.T) {
	t.Parallel()

	msg := newAtomicWriteMessage(t)
	dir := testutils.TempDir(t, tempDirPrefix)

	path, err := msg.WriteToDirectory(dir)
	if err != nil {
		t.Fatalf("WriteToDirectory: %v", err)
	}
	wantPath := filepath.Join(dir, fmtHashHex(msg.Hash))
	if path != wantPath {
		t.Fatalf("path=%q want=%q", path, wantPath)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if bytes.Contains([]byte(name), []byte(".tmp.")) {
			t.Fatalf("leftover tmp file %q in directory after write", name)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if _, err := msgpack.Unpack(data); err != nil {
		t.Fatalf("written file is not valid msgpack: %v", err)
	}
}

// TestWriteToDirectoryConcurrentReaderNeverSeesPartial covers Phase 16 task 1:
// WriteToDirectory must replace the destination file atomically (tmp file +
// rename), so a concurrent reader never observes a truncated or partial
// container. A direct os.WriteFile truncates then writes, exposing a window
// where the file is empty or partial; the tmp+rename approach closes it,
// matching Python LXMessage.write_to_directory (LXMessage.py:677-693, v0.9.9).
func TestWriteToDirectoryConcurrentReaderNeverSeesPartial(t *testing.T) {
	t.Parallel()

	msg := newAtomicWriteMessage(t)
	dir := testutils.TempDir(t, tempDirPrefix)
	finalPath := filepath.Join(dir, fmtHashHex(msg.Hash))

	// Seed an initial valid file so the reader has a baseline to observe.
	if _, err := msg.WriteToDirectory(dir); err != nil {
		t.Fatalf("seed WriteToDirectory: %v", err)
	}

	deadline := time.Now().Add(1500 * time.Millisecond)
	var wg sync.WaitGroup
	errCh := make(chan string, 1)
	reportErr := func(msg string) {
		select {
		case errCh <- msg:
		default:
		}
	}

	// Reader: every read must yield a complete, valid msgpack container.
	wg.Go(func() {
		for time.Now().Before(deadline) {
			data, err := os.ReadFile(finalPath)
			if err != nil {
				// A rename in flight can transiently make the read fail; retry.
				continue
			}
			if _, err := msgpack.Unpack(data); err != nil {
				reportErr("concurrent reader observed partial file: " + err.Error())
				return
			}
		}
	})

	// Writer: repeatedly overwrite via WriteToDirectory.
	wg.Go(func() {
		for time.Now().Before(deadline) {
			if _, err := msg.WriteToDirectory(dir); err != nil {
				reportErr("WriteToDirectory: " + err.Error())
				return
			}
		}
	})

	wg.Wait()
	select {
	case msg := <-errCh:
		t.Fatal(msg)
	default:
	}
}

// TestWriteToDirectoryConcurrentCallsDontCorrupt covers Phase 16 task 1: many
// concurrent WriteToDirectory calls on the same message serialize under the
// per-message persist mutex and leave exactly one valid final file with no
// tmp remnants, mirroring Python's __persist_lock (LXMessage.py:188, 679).
func TestWriteToDirectoryConcurrentCallsDontCorrupt(t *testing.T) {
	t.Parallel()

	msg := newAtomicWriteMessage(t)
	dir := testutils.TempDir(t, tempDirPrefix)

	const n = 32
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			if _, err := msg.WriteToDirectory(dir); err != nil {
				t.Errorf("WriteToDirectory: %v", err)
			}
		})
	}
	wg.Wait()

	finalPath := filepath.Join(dir, fmtHashHex(msg.Hash))
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("ReadFile final: %v", err)
	}
	if _, err := msgpack.Unpack(data); err != nil {
		t.Fatalf("final file is not valid msgpack after concurrent writes: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if bytes.Contains([]byte(e.Name()), []byte(".tmp.")) {
			t.Fatalf("leftover tmp file %q after concurrent writes", e.Name())
		}
	}
}

// fmtHashHex mirrors the hex formatting WriteToDirectory uses for the file
// name (fmt.Sprintf("%x", hash)).
func fmtHashHex(hash []byte) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, len(hash)*2)
	for _, b := range hash {
		out = append(out, hexDigits[b>>4], hexDigits[b&0x0f])
	}
	return string(out)
}
