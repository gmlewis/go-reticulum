// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

func TestFetchErrorMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		response    any
		status      int
		expectedMsg string
	}{
		{
			name:        "file not found on remote",
			response:    false,
			status:      rns.RequestReady,
			expectedMsg: "Fetch request failed, the file testfile.txt was not found on the remote",
		},
		{
			name:        "remote error",
			response:    nil,
			status:      rns.RequestReady,
			expectedMsg: "Fetch request failed due to an error on the remote system",
		},
		{
			name:        "fetch not allowed",
			response:    rns.ReqFetchNotAllowed,
			status:      rns.RequestReady,
			expectedMsg: "Fetch request failed, fetching the file testfile.txt was not allowed by the remote",
		},
		{
			name:        "unknown error",
			response:    nil,
			status:      rns.RequestSent,
			expectedMsg: "Fetch request failed due to an unknown error (probably not authorised)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr := &rns.RequestReceipt{
				Response: tt.response,
				Status:   tt.status,
			}

			fileName := "testfile.txt"
			msg := getFetchErrorMessage(rr, fileName)

			if !strings.Contains(msg, tt.expectedMsg) {
				t.Errorf("Expected message to contain %q, got %q", tt.expectedMsg, msg)
			}
		})
	}
}

func getFetchErrorMessage(rr *rns.RequestReceipt, fileName string) string {
	if rr.Status == rns.RequestReady {
		if rr.Response == false {
			return fmt.Sprintf("\r%v\rFetch request failed, the file %v was not found on the remote\n", strings.Repeat(" ", 60), fileName)
		} else if rr.Response == nil {
			return fmt.Sprintf("\r%v\rFetch request failed due to an error on the remote system\n", strings.Repeat(" ", 60))
		} else if rr.Response == rns.ReqFetchNotAllowed {
			return fmt.Sprintf("\r%v\rFetch request failed, fetching the file %v was not allowed by the remote\n", strings.Repeat(" ", 60), fileName)
		}
	}
	return fmt.Sprintf("\r%v\rFetch request failed due to an unknown error (probably not authorised)\n", strings.Repeat(" ", 60))
}
