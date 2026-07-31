// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"io"
	"sync"
)

type scriptedSerial struct {
	mu           sync.Mutex
	reads        []byte
	writes       [][]byte
	closed       bool
	blockOnEmpty bool
	wait         chan struct{}
	closeOnce    sync.Once
}

func (s *scriptedSerial) Read(data []byte) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, io.EOF
	}
	if len(s.reads) == 0 {
		if s.blockOnEmpty {
			wait := s.wait
			s.mu.Unlock()
			if wait != nil {
				<-wait
			}
			return 0, io.EOF
		}
		s.mu.Unlock()
		return 0, io.EOF
	}
	data[0] = s.reads[0]
	s.reads = s.reads[1:]
	s.mu.Unlock()
	return 1, nil
}

func (s *scriptedSerial) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyData := append([]byte(nil), data...)
	s.writes = append(s.writes, copyData)
	return len(data), nil
}

func (s *scriptedSerial) Close() error {
	s.mu.Lock()
	s.closed = true
	wait := s.wait
	s.mu.Unlock()

	s.closeOnce.Do(func() {
		if wait != nil {
			close(wait)
		}
	})
	return nil
}
