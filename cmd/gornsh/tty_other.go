// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !linux || windows

package main

type ttyRestorer struct {
	active bool
}

func newTTYRestorer(int) (*ttyRestorer, error) {
	return &ttyRestorer{}, nil
}

func (t *ttyRestorer) raw() error {
	return nil
}

func (t *ttyRestorer) restore() error {
	return nil
}
