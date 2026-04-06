// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

type fetchLinkResolverFunc func([]byte) *rns.Link

func (f fetchLinkResolverFunc) FindLink(linkID []byte) *rns.Link {
	return f(linkID)
}

func TestNewFetchRequestHandlerLogsThroughInjectedLogger(t *testing.T) {
	t.Parallel()

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogVerbose)
	var out bytes.Buffer
	logger.SetLogDest(rns.LogCallback)
	logger.SetLogCallback(func(msg string) {
		_, _ = out.WriteString(msg)
		_, _ = out.WriteString("\n")
	})

	handler := newFetchRequestHandler(logger, true, "", false, fetchLinkResolverFunc(func([]byte) *rns.Link { return nil }))
	response := handler("fetch_file", []byte("/tmp/missing.txt"), []byte("request-id"), []byte("link-id"), nil, time.Now())

	if response != false {
		t.Fatalf("response = %v, want false", response)
	}
	if got := out.String(); got == "" {
		t.Fatal("expected handler to log through the injected logger")
	}
}
