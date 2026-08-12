// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// newReticulumGitGroupClient prepares a client for a group-level
// (gperms) URL rns://<hash>/<group>, mirroring the group-permissions path
// of ReticulumGitClient (server.py) which calls parse_remote_group_url. It
// is like newReticulumGitClient but accepts a two-component group URL and
// leaves repoName/repoPath empty.
func newReticulumGitGroupClient(ts rns.Transport, configDir, identityPath, remoteURL string, logger *rns.Logger) (*reticulumGitClient, error) {
	destHash, group, err := parseRemoteGroupURL(remoteURL)
	if err != nil {
		return nil, err
	}

	if identityPath == "" {
		identityPath = filepath.Join(configDir, "client_identity")
	}
	identity, err := loadOrCreateIdentity(identityPath, logger)
	if err != nil {
		return nil, fmt.Errorf("could not load client identity: %w", err)
	}

	return &reticulumGitClient{
		transport:       ts,
		identity:        identity,
		destinationHash: destHash,
		groupName:       group,
		configDir:       configDir,
		identityPath:    identityPath,
	}, nil
}

// groupPermissions fetches and edits the group .allowed permissions, mirroring
// ReticulumGitClient.group_permissions (server.py). It sends a gperms get
// request, opens the editor on the current content, and sends a gperms set
// request with the edited content.
func (c *reticulumGitClient) groupPermissions() error {
	getData := map[any]any{
		int64(idxGroup): c.groupName,
		"operation":     "gperms",
		"step":          "get",
	}
	packed, err := msgpack.Pack(getData)
	if err != nil {
		return fmt.Errorf("could not pack gperms get: %w", err)
	}
	response, _, err := c.sendRequest(pathPerms, packed, requestTimeout)
	if err != nil {
		return err
	}
	currentContent, err := permsResponseContent(response)
	if err != nil {
		return err
	}

	content, err := editPermissions(currentContent)
	if err != nil {
		return err
	}
	if content == "" {
		fmt.Println("Edit cancelled")
		return nil
	}

	setData := map[any]any{
		int64(idxGroup): c.groupName,
		"operation":     "gperms",
		"step":          "set",
		"content":       content,
	}
	packed, err = msgpack.Pack(setData)
	if err != nil {
		return fmt.Errorf("could not pack gperms set: %w", err)
	}
	response, _, err = c.sendRequest(pathPerms, packed, requestTimeout)
	if err != nil {
		return err
	}
	if err := permsCheckOK(response); err != nil {
		return err
	}
	fmt.Printf("Permissions updated for group %s\n", c.groupName)
	return nil
}

// repositoryPermissions fetches and edits the repository .allowed
// permissions, mirroring ReticulumGitClient.repository_permissions
// (server.py). It sends an rperms get request, opens the editor on the
// current content, and sends an rperms set request with the edited content.
func (c *reticulumGitClient) repositoryPermissions() error {
	getData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "rperms",
		"step":               "get",
	}
	packed, err := msgpack.Pack(getData)
	if err != nil {
		return fmt.Errorf("could not pack rperms get: %w", err)
	}
	response, _, err := c.sendRequest(pathPerms, packed, requestTimeout)
	if err != nil {
		return err
	}
	currentContent, err := permsResponseContent(response)
	if err != nil {
		return err
	}

	content, err := editPermissions(currentContent)
	if err != nil {
		return err
	}
	if content == "" {
		fmt.Println("Edit cancelled")
		return nil
	}

	setData := map[any]any{
		int64(idxRepository): c.repoPath,
		"operation":          "rperms",
		"step":               "set",
		"content":            content,
	}
	packed, err = msgpack.Pack(setData)
	if err != nil {
		return fmt.Errorf("could not pack rperms set: %w", err)
	}
	response, _, err = c.sendRequest(pathPerms, packed, requestTimeout)
	if err != nil {
		return err
	}
	if err := permsCheckOK(response); err != nil {
		return err
	}
	fmt.Printf("Permissions updated for %s\n", c.repoPath)
	return nil
}

// permsResponseContent extracts the "content" string from a perms get
// response, mirroring the get half of group_permissions /
// repository_permissions (server.py). The response is a status byte
// followed by a msgpack map {"content": str}.
func permsResponseContent(response any) (string, error) {
	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return "", errors.New("No response from remote")
	}
	if respBytes[0] != resOK {
		return "", fmt.Errorf("Remote error: %s", string(respBytes[1:]))
	}
	if len(respBytes) > 1 {
		unpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
		if err == nil {
			if m, ok := unpacked.(map[any]any); ok {
				if s, ok := m["content"].(string); ok {
					return s, nil
				}
			}
		}
	}
	return "", nil
}

// permsCheckOK verifies a perms set response carries the OK status byte,
// mirroring the set half of group_permissions / repository_permissions
// (server.py).
func permsCheckOK(response any) error {
	respBytes, ok := response.([]byte)
	if !ok || len(respBytes) == 0 {
		return errors.New("No response from remote")
	}
	if respBytes[0] != resOK {
		return fmt.Errorf("Remote error: %s", string(respBytes[1:]))
	}
	return nil
}
