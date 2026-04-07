// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

var errRemoteOperationUnavailable = errors.New("remote operation unavailable")
var errPathRequestTimedOut = errors.New("path request timed out")
var errRemoteRequestFailed = errors.New("the remote request failed")

type remotePurpose string

const (
	remotePurposeManagement remotePurpose = "management"
	remotePurposeBlackhole  remotePurpose = "blackhole"
)

type remoteRequestClient interface {
	Request(path string, data any, timeout float64) (any, error)
	Close() error
}

type remoteRequestFunc func(path string, data any, timeout float64) (any, error)

func (f remoteRequestFunc) Request(path string, data any, timeout float64) (any, error) {
	return f(path, data, timeout)
}

func (f remoteRequestFunc) Close() error { return nil }

type remoteLinkClient struct {
	link *rns.Link
}

func (c *remoteLinkClient) Request(path string, data any, timeout float64) (any, error) {
	if c == nil || c.link == nil {
		return nil, errors.New("remote link is unavailable")
	}
	receipt, err := c.link.Request(path, data, nil, nil, nil, time.Duration(timeout*float64(time.Second)))
	if err != nil {
		return nil, err
	}
	if receipt == nil {
		return nil, errors.New("remote request was not sent")
	}
	deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
	for {
		switch receipt.GetStatus() {
		case rns.RequestReady:
			return receipt.Response, nil
		case rns.RequestFailed:
			return nil, errRemoteRequestFailed
		}
		if time.Now().After(deadline) {
			return nil, errRemoteRequestFailed
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (c *remoteLinkClient) Close() error {
	if c == nil || c.link == nil {
		return nil
	}
	c.link.Teardown()
	return nil
}

func (rt *runtimeT) connectRemoteClient(out io.Writer, ts rns.Transport, remoteHash []byte, identityPath string, timeout float64, purpose remotePurpose, noOutput bool) (remoteRequestClient, error) {
	if !ts.HasPath(remoteHash) {
		if !noOutput {
			if _, err := fmt.Fprintf(out, "Path to %v requested  ", rns.PrettyHex(remoteHash)); err != nil {
				return nil, err
			}
		}
		if err := ts.RequestPath(remoteHash); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
		spinner := []string{"⢄", "⢂", "⢁", "⡁", "⡈", "⡐", "⡠"}
		index := 0
		for !ts.HasPath(remoteHash) {
			if time.Now().After(deadline) {
				if !noOutput {
					if _, err := fmt.Fprint(out, "\r                                                          \rPath request timed out\n"); err != nil {
						return nil, err
					}
				}
				return nil, errPathRequestTimedOut
			}
			time.Sleep(100 * time.Millisecond)
			if !noOutput {
				if _, err := fmt.Fprintf(out, "\b\b%v ", spinner[index]); err != nil {
					return nil, err
				}
			}
			index = (index + 1) % len(spinner)
		}
	}

	remoteIdentity := ts.Recall(remoteHash)
	if remoteIdentity == nil {
		return nil, fmt.Errorf("Invalid destination entered. Check your input.")
	}

	if !noOutput {
		if _, err := fmt.Fprint(out, "\r                                                          \rEstablishing link with remote transport instance... "); err != nil {
			return nil, err
		}
	}

	remoteDestination, err := buildRemoteDestination(ts, remoteIdentity, purpose)
	if err != nil {
		return nil, err
	}

	link, err := rns.NewLink(ts, remoteDestination)
	if err != nil {
		return nil, err
	}

	var authIdentity *rns.Identity
	if purpose == remotePurposeManagement {
		if identityPath == "" {
			return nil, fmt.Errorf("Could not load management identity from %v", identityPath)
		}
		authIdentity, err = rt.loadIdentityFromPath(identityPath)
		if err != nil {
			return nil, fmt.Errorf("Could not load management identity from %v", identityPath)
		}
	}

	link.SetLinkEstablishedCallback(func(l *rns.Link) {
		if authIdentity != nil {
			_ = l.Identify(authIdentity)
		}
	})

	if err := link.Establish(); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
	for {
		switch link.GetStatus() {
		case rns.LinkActive:
			return &remoteLinkClient{link: link}, nil
		case rns.LinkClosed:
			return nil, fmt.Errorf("link closed")
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("link establishment timed out")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func buildRemoteDestination(ts rns.Transport, identity *rns.Identity, purpose remotePurpose) (*rns.Destination, error) {
	switch purpose {
	case remotePurposeManagement:
		return rns.NewDestination(ts, identity, rns.DestinationOut, rns.DestinationSingle, "rnstransport", "remote", "management")
	case remotePurposeBlackhole:
		return rns.NewDestination(ts, identity, rns.DestinationOut, rns.DestinationSingle, "rnstransport", "info", "blackhole")
	default:
		return nil, fmt.Errorf("unknown remote purpose %v", purpose)
	}
}

func (rt *runtimeT) loadIdentityFromPath(identityPath string) (*rns.Identity, error) {
	if identityPath == "" {
		return nil, fmt.Errorf("identity path is required")
	}
	resolvedPath := identityPath
	if len(identityPath) > 0 && identityPath[0] == '~' {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		resolvedPath = filepath.Join(homeDir, identityPath[1:])
	}
	return rns.FromFile(resolvedPath, rt.logger)
}

func doRemoteTable(out io.Writer, client remoteRequestClient, destinationHash []byte, maxHops int, jsonOut bool, timeout float64) error {
	response, err := client.Request("/path", []any{"table", destinationHash, maxHops}, timeout)
	if err != nil {
		return err
	}
	entries, ok := response.([]any)
	if !ok {
		return fmt.Errorf("remote path table response has unexpected type %T", response)
	}
	rendered, err := renderPathTable(entriesToPathInfo(entries), maxHops, jsonOut, nil)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(out, rendered)
	return err
}

func doRemoteRates(out io.Writer, client remoteRequestClient, destinationHash []byte, jsonOut bool, timeout float64) error {
	response, err := client.Request("/path", []any{"rates", destinationHash}, timeout)
	if err != nil {
		return err
	}
	entries, ok := response.([]any)
	if !ok {
		return fmt.Errorf("remote rate table response has unexpected type %T", response)
	}
	rendered, err := renderRateTable(entries, time.Now(), nil, jsonOut)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(out, rendered)
	return err
}

func doRemoteBlackholedList(out io.Writer, client remoteRequestClient, filter string, localIdentityHash []byte, jsonOut bool, timeout float64) error {
	response, err := client.Request("/list", nil, timeout)
	if err != nil {
		return err
	}
	rows, err := normalizeRemoteBlackholedResponse(response)
	if err != nil {
		return err
	}
	entries := make([]any, 0, len(rows))
	for _, entry := range rows {
		entries = append(entries, entry)
	}
	rendered, err := renderBlackholedIdentities(entries, time.Now(), filter, localIdentityHash)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(out, rendered)
	return err
}

func entriesToPathInfo(entries []any) []rns.PathInfo {
	paths := make([]rns.PathInfo, 0, len(entries))
	for _, entry := range entries {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		hash, _ := asBytes(row["hash"])
		via, _ := asBytes(row["via"])
		hops, _ := asInt(row["hops"])
		expiresFloat, _ := asFloat64(row["expires"])
		timestampFloat, _ := asFloat64(row["timestamp"])
		ifName, _ := row["interface"].(string)
		paths = append(paths, rns.PathInfo{
			Timestamp: time.Unix(0, int64(timestampFloat*1e9)),
			Hash:      hash,
			NextHop:   via,
			Hops:      hops,
			Interface: pathTableTestInterface{name: ifName},
			Expires:   time.Unix(0, int64(expiresFloat*1e9)),
		})
	}
	return paths
}

func normalizeRemoteBlackholedResponse(response any) ([]map[string]any, error) {
	switch rows := response.(type) {
	case map[string]any:
		out := make([]map[string]any, 0, len(rows))
		for identityHash, entry := range rows {
			item, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			item = cloneMap(item)
			item["identity_hash"] = decodeRemoteIdentityHash(identityHash)
			out = append(out, item)
		}
		return out, nil
	case map[any]any:
		out := make([]map[string]any, 0, len(rows))
		for identityHash, entry := range rows {
			item, ok := entry.(map[any]any)
			if !ok {
				continue
			}
			converted := make(map[string]any, len(item)+1)
			for key, value := range item {
				keyStr, ok := key.(string)
				if !ok {
					continue
				}
				converted[keyStr] = value
			}
			converted["identity_hash"] = decodeRemoteIdentityHash(identityHash)
			out = append(out, converted)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("remote blackhole list response has unexpected type %T", response)
	}
}

func decodeRemoteIdentityHash(value any) []byte {
	switch hash := value.(type) {
	case []byte:
		return append([]byte(nil), hash...)
	case string:
		if decoded, err := hex.DecodeString(hash); err == nil {
			return decoded
		}
		return []byte(hash)
	default:
		return nil
	}
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
