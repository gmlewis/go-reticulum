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
	"strconv"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

type fetchLinkFinder interface {
	FindLink(linkID []byte) *rns.Link
}

func newFetchRequestHandler(allowFetch bool, jail string, noCompress bool, linkFinder fetchLinkFinder) func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
	return func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *rns.Identity, requestedAt time.Time) any {
		rns.Logf("FETCH_HANDLER CALLED: allowFetch=%v, path=%v, data=%v, requestID=%x", rns.LogVerbose, false, allowFetch, path, string(data), requestID)
		if !allowFetch {
			rns.Logf("fetch not allowed, returning 0xF0", rns.LogVerbose, false)
			return byte(0xF0)
		}

		if jail != "" {
			dataStr := string(data)
			if !strings.HasPrefix(dataStr, jail+"/") {
				dataStr = strings.TrimPrefix(dataStr, jail+"/")
			}
			filePath := filepath.Clean(filepath.Join(jail, dataStr))
			if !strings.HasPrefix(filePath, jail+"/") {
				rns.Logf("Disallowing fetch request for %v outside of fetch jail %v", rns.LogWarning, false, filePath, jail)
				return byte(0xF0)
			}
			data = []byte(filePath)
		}

		filePath := string(data)
		if _, err := os.Stat(filePath); err != nil {
			rns.Logf("Client-requested file not found: %v", rns.LogVerbose, false, filePath)
			return false
		}

		link := linkFinder.FindLink(linkID)
		if link == nil {
			rns.Logf("Link not found for request %x", rns.LogError, false, requestID)
			return nil
		}

		fileData, err := os.ReadFile(filePath)
		if err != nil {
			rns.Logf("Could not read file %v: %v", rns.LogError, false, filePath, err)
			return false
		}

		metadata := map[string][]byte{
			"name": []byte(filepath.Base(filePath)),
		}

		resource, err := rns.NewResourceWithOptions(fileData, link, rns.ResourceOptions{
			AutoCompress: !noCompress,
			Metadata:     metadata,
		})
		if err != nil {
			rns.Logf("Could not create resource: %v", rns.LogError, false, err)
			return false
		}
		if err := resource.Advertise(); err != nil {
			rns.Logf("Could not advertise resource: %v", rns.LogError, false, err)
			return false
		}

		rns.Logf("Sending file %v to client", rns.LogVerbose, false, filePath)
		return true
	}
}

func (a *appT) doListen(ts rns.Transport) {
	logger := a.getLogger()
	idPath := a.identityPath
	noCompress := a.noCompress
	allowFetch := a.allowFetch
	jail := a.jail
	savePath := a.savePath
	overwrite := a.overwrite
	announceInterval := a.announceInterval
	allowed := a.allowed
	noAuth := a.noAuth
	printIdentity := a.printIdentity
	if idPath == "" {
		home, _ := os.UserHomeDir()
		idPath = filepath.Join(home, ".reticulum", "identities", AppName)
	}

	var id *rns.Identity
	if _, err := os.Stat(idPath); err == nil {
		id, err = rns.FromFile(idPath)
		if err != nil {
			logger.Log(fmt.Sprintf("Could not load identity for rncp. The identity file at \"%v\" may be corrupt or unreadable.", idPath), rns.LogError, false)
			os.Exit(2)
		}
	} else {
		logger.Log("No valid saved identity found, creating new...", rns.LogInfo, false)
		var err error
		id, err = rns.NewIdentity(true)
		if err != nil {
			log.Fatalf("Could not create new identity: %v\n", err)
		}
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
		if err := f.Close(); err != nil {
			rns.Logf("Warning: Could not close temporary write test file %v: %v", rns.LogWarning, false, tmpFile, err)
		}
		if err := os.Remove(tmpFile); err != nil {
			rns.Logf("Warning: Could not remove temporary write test file %v: %v", rns.LogWarning, false, tmpFile, err)
		}
		rns.Logf("Saving received files in %q", rns.LogVerbose, false, sp)
	}

	if overwrite {
		rns.Log("Allowing overwrite of received files", rns.LogVerbose, false)
	}

	if jail != "" {
		fetchJail := filepath.Clean(jail)
		rns.Logf("Restricting fetch requests to paths under %q", rns.LogVerbose, false, fetchJail)
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

	// Always register fetch handler, but check allowFetch inside (matches Python behavior)
	rns.Logf("Registering fetch_file handler, allowFetch=%v", rns.LogVerbose, false, allowFetch)
	dest.RegisterRequestHandler("fetch_file", newFetchRequestHandler(allowFetch, jail, noCompress, ts), rns.AllowAll, nil, !noCompress)

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
			idStr := ""
			if remoteID := l.GetRemoteIdentity(); remoteID != nil {
				idStr = " from " + rns.PrettyHex(remoteID.Hash)
			}
			rns.Logf("Starting resource transfer %x%s", rns.LogInfo, false, res.Hash(), idStr)
		})
		l.SetResourceConcludedCallback(func(res *rns.Resource) {
			if res.Status() == rns.ResourceStatusComplete {
				rns.Logf("%v completed", rns.LogInfo, false, res)

				metadata := res.Metadata()
				if metadata == nil {
					rns.Log("Invalid data received, ignoring resource", rns.LogError, false)
					return
				}

				nameBytes, ok := metadata["name"]
				if !ok {
					rns.Log("Invalid data received, ignoring resource", rns.LogError, false)
					return
				}

				filename := filepath.Base(string(nameBytes))
				counter := 0
				var savedFilename string

				if savePath != "" {
					savedFilename = filepath.Clean(filepath.Join(savePath, filename))
					if !strings.HasPrefix(savedFilename, savePath+"/") {
						rns.Logf("Invalid save path %v, ignoring", rns.LogError, false, savedFilename)
						return
					}
				} else {
					savedFilename = filename
				}

				fullSavePath := savedFilename
				if overwrite {
					if _, err := os.Stat(fullSavePath); err == nil {
						if err := os.Remove(fullSavePath); err != nil {
							rns.Logf("Could not overwrite existing file %v, renaming instead", rns.LogError, false, fullSavePath)
							overwrite = false
						}
					}
				}

				for {
					if _, err := os.Stat(fullSavePath); os.IsNotExist(err) {
						break
					}
					counter++
					fullSavePath = savedFilename + "." + strconv.Itoa(counter)
				}

				if err := os.WriteFile(fullSavePath, res.Data(), 0o644); err != nil {
					rns.Logf("An error occurred while saving received resource: %v", rns.LogError, false, err)
					return
				}

				rns.Logf("Saved resource to %v", rns.LogVerbose, false, fullSavePath)
			} else {
				rns.Log("Resource failed", rns.LogError, false)
			}
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
