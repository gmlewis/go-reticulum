// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"time"
)

const (
	deviceProbeInitialDelay = 2500 * time.Millisecond
	deviceProbeFinalDelay   = 750 * time.Millisecond
)

type deviceProbeDetector interface {
	Detect() error
	Detected() bool
}

type deviceProbeSleeper interface {
	Sleep(time.Duration)
}

type deviceProbeState struct {
	detector deviceProbeDetector
	sleeper  deviceProbeSleeper
}

func newDeviceProbeState(detector deviceProbeDetector, sleeper deviceProbeSleeper) *deviceProbeState {
	return &deviceProbeState{detector: detector, sleeper: sleeper}
}

func (p *deviceProbeState) probe() (bool, error) {
	p.sleeper.Sleep(deviceProbeInitialDelay)
	if err := p.detector.Detect(); err != nil {
		return false, err
	}
	p.sleeper.Sleep(deviceProbeFinalDelay)
	if p.detector.Detected() {
		return true, nil
	}
	return false, errors.New("Got invalid response while detecting device")
}
