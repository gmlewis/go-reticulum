// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

func TestFetchModeSavesReceivedFiles(t *testing.T) {
	t.Parallel()
	skipShortIntegration(t)

	// Create temp directories
	listenerDir, cleanup1 := tempDir(t)
	defer cleanup1()
	fetcherDir, cleanup2 := tempDir(t)
	defer cleanup2()
	saveDir, cleanup3 := tempDir(t)
	defer cleanup3()
	testFileDir, cleanup4 := tempDir(t)
	defer cleanup4()

	// Create test file on listener side
	testContent := []byte("Hello from listener!")
	testFilePath := filepath.Join(testFileDir, "testfile.txt")
	if err := os.WriteFile(testFilePath, testContent, 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create identities
	listenerID := mustCreateIdentity(t, listenerDir, "listener")
	fetcherID := mustCreateIdentity(t, fetcherDir, "fetcher")

	// Create transport systems with identities
	listenerTS := rns.NewTransportSystem()
	listenerTS.SetNetworkIdentity(listenerID)
	fetcherTS := rns.NewTransportSystem()
	fetcherTS.SetNetworkIdentity(fetcherID)

	// Create pipe interfaces
	pipeA := interfaces.NewPipeInterface("listener", func(data []byte, iface interfaces.Interface) {
		listenerTS.Inbound(data, iface)
	})
	pipeB := interfaces.NewPipeInterface("fetcher", func(data []byte, iface interfaces.Interface) {
		fetcherTS.Inbound(data, iface)
	})
	pipeA.SetOther(pipeB)
	pipeB.SetOther(pipeA)
	listenerTS.RegisterInterface(pipeA)
	fetcherTS.RegisterInterface(pipeB)
	defer func() {
		_ = pipeA.Detach()
		_ = pipeB.Detach()
	}()

	// Create listener destination with fetch handler
	listenerDest, err := rns.NewDestination(listenerTS, listenerID, rns.DestinationIn, rns.DestinationSingle, "test", "receive")
	if err != nil {
		t.Fatalf("Failed to create listener destination: %v", err)
	}

	// Track receiver-side link establishment
	listenerLinkEstablished := make(chan *rns.Link, 1)
	listenerDest.SetLinkEstablishedCallback(func(l *rns.Link) {
		listenerLinkEstablished <- l
	})

	// Register fetch handler that reads the test file
	listenerDest.RegisterRequestHandler("fetch_file", func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
		fileData, err := os.ReadFile(testFilePath)
		if err != nil {
			t.Logf("Could not read file: %v", err)
			return false
		}

		// Find link and send resource with metadata
		link := listenerTS.FindLink(linkID)
		if link == nil {
			t.Logf("Link not found")
			return false
		}

		metadata := map[string][]byte{
			"name": []byte(filepath.Base(testFilePath)),
		}

		resource, err := rns.NewResourceWithOptions(fileData, link, rns.ResourceOptions{
			AutoCompress: false,
			Metadata:     metadata,
		})
		if err != nil {
			t.Logf("Could not create resource: %v", err)
			return false
		}
		if err := resource.Advertise(); err != nil {
			t.Logf("Could not advertise: %v", err)
			return false
		}
		return true
	}, rns.AllowAll, nil, false)

	// Create link - must target the listener destination (receiver side)
	link, err := rns.NewLink(fetcherTS, listenerDest)
	if err != nil {
		t.Fatalf("Failed to create link: %v", err)
	}

	// Set callback BEFORE establishing
	established := make(chan bool, 1)
	link.SetLinkEstablishedCallback(func(l *rns.Link) {
		established <- true
	})

	if err := link.Establish(); err != nil {
		t.Fatalf("Failed to establish link: %v", err)
	}

	select {
	case <-established:
	case <-time.After(5 * time.Second):
		t.Fatal("Link establishment timed out")
	}

	// Wait for receiver-side link to be established
	select {
	case <-listenerLinkEstablished:
	case <-time.After(5 * time.Second):
		t.Fatal("Receiver link establishment timed out")
	}

	// Identify
	if err := link.Identify(fetcherID); err != nil {
		t.Fatalf("Failed to identify: %v", err)
	}

	// Set up resource callback to save file
	savedFileChan := make(chan string, 1)
	if err := link.SetResourceStrategy(rns.AcceptAll); err != nil {
		t.Fatalf("Failed to set resource strategy: %v", err)
	}
	link.SetResourceCallback(func(adv *rns.ResourceAdvertisement) bool {
		return true
	})
	link.SetResourceStartedCallback(func(res *rns.Resource) {
		res.SetCallback(func(r *rns.Resource) {
			if r.Status() == rns.ResourceStatusComplete {
				md := r.Metadata()
				if md == nil {
					t.Errorf("Expected metadata")
					savedFileChan <- ""
					return
				}
				nameBytes, ok := md["name"]
				if !ok {
					t.Errorf("Expected 'name' in metadata")
					savedFileChan <- ""
					return
				}
				filename := string(nameBytes)
				savedFilePath := filepath.Join(saveDir, filename)
				if err := os.WriteFile(savedFilePath, r.Data(), 0o644); err != nil {
					t.Errorf("Failed to save file: %v", err)
					savedFileChan <- ""
					return
				}
				savedFileChan <- savedFilePath
			}
		})
	})

	// Send fetch request
	_, err = link.Request("fetch_file", []byte("testfile.txt"), func(rr *rns.RequestReceipt) {
		if rr.Status != rns.RequestReady {
			t.Logf("Request failed with status: %v", rr.Status)
		}
	}, nil, nil, 0)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	// Wait for file to be saved
	var savedFilePath string
	select {
	case savedFilePath = <-savedFileChan:
	case <-time.After(30 * time.Second):
		t.Fatal("Timed out waiting for file to be saved")
	}

	// Verify file was saved
	if savedFilePath == "" {
		t.Fatal("File was not saved")
	}

	if _, err := os.Stat(savedFilePath); os.IsNotExist(err) {
		t.Fatalf("Saved file does not exist: %s", savedFilePath)
	}

	savedContent, err := os.ReadFile(savedFilePath)
	if err != nil {
		t.Fatalf("Failed to read saved file: %v", err)
	}

	if string(savedContent) != string(testContent) {
		t.Fatalf("Saved content differs: got %q, want %q", string(savedContent), string(testContent))
	}

	link.Teardown()
}

func mustCreateIdentity(t *testing.T, dir, name string) *rns.Identity {
	t.Helper()
	idPath := filepath.Join(dir, "identities", name)
	if err := os.MkdirAll(filepath.Dir(idPath), 0o700); err != nil {
		t.Fatalf("Failed to create identity dir: %v", err)
	}
	id, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("Failed to create identity: %v", err)
	}
	if err := id.ToFile(idPath); err != nil {
		t.Fatalf("Failed to save identity: %v", err)
	}
	loaded, err := rns.FromFile(idPath)
	if err != nil {
		t.Fatalf("Failed to load identity: %v", err)
	}
	return loaded
}
