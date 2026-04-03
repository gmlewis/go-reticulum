// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import "time"

func sleepAfterFetchFailure(sleep func(time.Duration)) {
	sleep(150 * time.Millisecond)
}

func sleepAfterFetchCompletion(sleep func(time.Duration)) {
	sleep(100 * time.Millisecond)
}

func sleepAfterSendCompletion(sleep func(time.Duration)) {
	sleep(250 * time.Millisecond)
}
