// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

type ttyRestorer struct {
	active    bool
	rawFn     func() error
	restoreFn func() error
}

func (t *ttyRestorer) raw() error {
	if t == nil || t.rawFn == nil {
		return nil
	}
	return t.rawFn()
}

func (t *ttyRestorer) restore() error {
	if t == nil || t.restoreFn == nil {
		return nil
	}
	return t.restoreFn()
}
