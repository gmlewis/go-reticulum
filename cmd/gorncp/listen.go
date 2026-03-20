// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

func doListen(ts rns.Transport, idPath string, noCompress bool, silent bool, allowFetch bool, jail string, savePath string, overwrite bool, announceInterval int, allowed []string, noAuth bool, printIdentity bool) {
	if idPath == "" {
		home, _ := os.UserHomeDir()
		idPath = filepath.Join(home, ".reticulum", "identities", AppName)
	}

	var id *rns.Identity
	if _, err := os.Stat(idPath); err == nil {
		id, err = rns.FromFile(idPath)
		if err != nil {
			rns.Logf("Could not load identity for rncp. The identity file at \"%v\" may be corrupt or unreadable.", rns.LogError, false, idPath)
			os.Exit(2)
		}
	} else {
		rns.Log("No valid saved identity found, creating new...", rns.LogInfo, false)
		id, _ = rns.NewIdentity(true)
		if err := id.ToFile(idPath); err != nil {
			log.Fatalf("Could not persist identity %q: %v\n", idPath, err)
		}
	}

	dest, err := rns.NewDestination(ts, id, rns.DestinationIn, rns.DestinationSingle, AppName, "receive")
	if err != nil {
		log.Fatalf("Could not create destination: %v\n", err)
	}

	// Build allowed identity hashes list
	var allowedIdentityHashes [][]byte
	destLen := (rns.TruncatedHashLength / 8) * 2

	// Get home directory for searching allowed identities file
	homeDir, _ := os.UserHomeDir()

	// Load allowed identities from file
	allowedFileName := "allowed_identities"
	var allowedFile string
	if _, err := os.Stat("/etc/rncp/" + allowedFileName); err == nil {
		allowedFile = "/etc/rncp/" + allowedFileName
	} else if _, err := os.Stat(filepath.Join(homeDir, ".config", "rncp", allowedFileName)); err == nil {
		allowedFile = filepath.Join(homeDir, ".config", "rncp", allowedFileName)
	} else if _, err := os.Stat(filepath.Join(homeDir, ".rncp", allowedFileName)); err == nil {
		allowedFile = filepath.Join(homeDir, ".rncp", allowedFileName)
	}

	if allowedFile != "" {
		data, err := os.ReadFile(allowedFile)
		if err != nil {
			rns.Logf("Error while parsing allowed_identities file: %v", rns.LogError, false, err)
		} else {
			lines := strings.ReplaceAll(string(data), "\r", "")
			parts := strings.Split(lines, "\n")
			var fileAllowed []string
			for _, a := range parts {
				if len(a) == destLen {
					fileAllowed = append(fileAllowed, a)
				}
			}
			if len(fileAllowed) > 0 {
				if len(allowed) == 0 {
					allowed = fileAllowed
				} else {
					allowed = append(allowed, fileAllowed...)
				}
				suffix := "y"
				if len(fileAllowed) > 1 {
					suffix = "ies"
				}
				rns.Logf("Loaded %d allowed identit%s from %v", rns.LogVerbose, false, len(fileAllowed), suffix, allowedFile)
			}
		}
	}

	// Validate and build allowed identity hashes
	for _, a := range allowed {
		if len(a) != destLen {
			fmt.Fprintf(os.Stderr, "Allowed destination length is invalid, must be %d hexadecimal characters (%d bytes).\n", destLen, destLen/2)
			os.Exit(1)
		}
		h, err := rns.HexToBytes(a)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid destination entered. Check your input.\n")
			os.Exit(1)
		}
		if h != nil {
			allowedIdentityHashes = append(allowedIdentityHashes, h)
		}
	}

	if savePath != "" {
		sp := filepath.Clean(savePath)
		if _, err := os.Stat(sp); err != nil {
			rns.Logf("Output directory not found", rns.LogError, false)
			os.Exit(3)
		}
		// Test if directory is writable by trying to open a temp file
		tmpFile := filepath.Join(sp, ".gorncp_write_test")
		f, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			rns.Logf("Output directory not writable", rns.LogError, false)
			os.Exit(4)
		}
		_ = f.Close()
		_ = os.Remove(tmpFile)
		rns.Logf("Saving received files in %q", rns.LogVerbose, false, sp)
	}

	if overwrite {
		rns.Log("Allowing overwrite of received files", rns.LogVerbose, false)
	}

	if jail != "" {
		fetchJail := filepath.Clean(jail)
		rns.Logf("Restricting fetch requests to paths under %q", rns.LogVerbose, false, fetchJail)
	}

	if savePath != "" {
		sp := filepath.Clean(savePath)
		if _, err := os.Stat(sp); err != nil {
			rns.Logf("Output directory not found", rns.LogError, false)
			os.Exit(3)
		}
		// Test if directory is writable by trying to open a temp file
		tmpFile := filepath.Join(sp, ".gorncp_write_test")
		f, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			rns.Logf("Output directory not writable", rns.LogError, false)
			os.Exit(4)
		}
		_ = f.Close()
		_ = os.Remove(tmpFile)
		rns.Logf("Saving received files in %q", rns.LogVerbose, false, sp)
	}

	if overwrite {
		rns.Log("Allowing overwrite of received files", rns.LogVerbose, false)
	}

	if len(allowed) > 0 {
		rns.Logf("Allowing %d identity hash(es)", rns.LogVerbose, false, len(allowed))
		for _, a := range allowed {
			rns.Logf("  Allowed: %v", rns.LogVerbose, false, a)
		}
	}

	if noAuth {
		rns.Log("Accepting unauthenticated requests", rns.LogVerbose, false)
	} else if len(allowedIdentityHashes) == 0 {
		fmt.Println("Warning: No allowed identities configured, rncp will not accept any files!")
	}

	if printIdentity {
		fmt.Printf("Identity     : %v\n", id)
		fmt.Printf("Listening on : %v\n", rns.PrettyHex(dest.Hash))
		os.Exit(0)
	}

	if allowFetch {
		dest.RegisterRequestHandler("fetch_file", func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
			// Check if fetch is allowed
			if !allowFetch {
				return byte(0xF0) // REQ_FETCH_NOT_ALLOWED
			}

			// Apply fetch jail validation
			if jail != "" {
				dataStr := string(data)
				if !strings.HasPrefix(dataStr, jail+"/") {
					dataStr = strings.TrimPrefix(dataStr, jail+"/")
				}
				filePath := filepath.Clean(filepath.Join(jail, dataStr))
				if !strings.HasPrefix(filePath, jail+"/") {
					rns.Logf("Disallowing fetch request for %v outside of fetch jail %v", rns.LogWarning, false, filePath, jail)
					return byte(0xF0) // REQ_FETCH_NOT_ALLOWED
				}
				data = []byte(filePath)
			}

			// Check file existence
			filePath := string(data)
			if _, err := os.Stat(filePath); err != nil {
				rns.Logf("Client-requested file not found: %v", rns.LogVerbose, false, filePath)
				return false
			}

			// Find the link and send the resource with metadata
			link := ts.FindLink(linkID)
			if link == nil {
				rns.Logf("Link not found for request %x", rns.LogError, false, requestID)
				return false
			}

			// Read the file
			fileData, err := os.ReadFile(filePath)
			if err != nil {
				rns.Logf("Could not read file %v: %v", rns.LogError, false, filePath, err)
				return false
			}

			// Create metadata with filename
			metadata := map[string][]byte{
				"name": []byte(filepath.Base(filePath)),
			}

			// Create and send resource with metadata
			resource, err := rns.NewResourceWithOptions(fileData, link, rns.ResourceOptions{
				AutoCompress: !noCompress,
				Metadata:     metadata,
			})
			if err != nil {
				rns.Logf("Could not create resource: %v", rns.LogError, false, err)
				return false
			}
			resource.SetRequestID(requestID)
			resource.SetResponse(true)
			if err := resource.Advertise(); err != nil {
				rns.Logf("Could not advertise resource: %v", rns.LogError, false, err)
				return false
			}

			rns.Logf("Sending file %v to client", rns.LogVerbose, false, filePath)
			return nil // Resource already sent
		}, rns.AllowAll, nil, !noCompress)
	}

	dest.SetLinkEstablishedCallback(func(l *rns.Link) {
		rns.Log("Incoming link established", rns.LogVerbose, false)
		l.SetRemoteIdentifiedCallback(func(link *rns.Link, identity *rns.Identity) {
			if identity != nil {
				found := false
				for _, h := range allowedIdentityHashes {
					if string(h) == string(identity.Hash) {
						found = true
						break
					}
				}
				if found {
					rns.Log("Authenticated sender", rns.LogVerbose, false)
				} else {
					if !noAuth {
						rns.Log("Sender not allowed, tearing down link", rns.LogVerbose, false)
						link.Teardown()
					}
				}
			}
		})
		if err := l.SetResourceStrategy(rns.AcceptApp); err != nil {
			log.Fatalf("l.SetResourceStrategy: %v", err)
		}
		l.SetResourceCallback(func(adv *rns.ResourceAdvertisement) bool {
			senderIdentity := l.GetRemoteIdentity()
			if senderIdentity != nil {
				for _, h := range allowedIdentityHashes {
					if string(h) == string(senderIdentity.Hash) {
						return true
					}
				}
			}
			if noAuth {
				return true
			}
			return false
		})
		l.SetResourceStartedCallback(func(res *rns.Resource) {
			rns.Log("Starting resource transfer", rns.LogInfo, false)
		})
		l.SetResourceConcludedCallback(func(res *rns.Resource) {
			rns.Logf("Resource concluded: %x", rns.LogInfo, false, res.Hash())
		})
	})

	fmt.Printf("Listening on : <%x>\n", dest.Hash)

	if announceInterval >= 0 {
		rns.Logf("Announcing destination (interval=%v)", rns.LogVerbose, false, announceInterval)
		if err := dest.Announce(nil); err != nil {
			rns.Logf("Announce failed: %v", rns.LogError, false, err)
		}
		if announceInterval > 0 {
			go func() {
				for {
					time.Sleep(time.Duration(announceInterval) * time.Second)
					if err := dest.Announce(nil); err != nil {
						rns.Logf("Announce failed: %v", rns.LogError, false, err)
					}
				}
			}()
		}
	}

	// Keep alive
	select {}
}
