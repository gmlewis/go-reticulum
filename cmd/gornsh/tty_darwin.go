// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build darwin

package main

/*
#include <termios.h>
#include <unistd.h>
*/
import "C"

import "fmt"

func newTTYRestorer(fd int) (*ttyRestorer, error) {
	if fd < 0 {
		return &ttyRestorer{}, nil
	}

	var saved C.struct_termios
	if _, err := C.tcgetattr(C.int(fd), &saved); err != nil {
		return &ttyRestorer{}, nil
	}

	restorer := &ttyRestorer{active: true}
	restorer.rawFn = func() error {
		raw := saved
		raw.c_iflag &^= C.tcflag_t(C.BRKINT | C.ICRNL | C.INPCK | C.ISTRIP | C.IXON)
		raw.c_oflag &^= C.tcflag_t(C.OPOST)
		raw.c_lflag &^= C.tcflag_t(C.ECHO | C.ICANON | C.IEXTEN | C.ISIG)
		raw.c_cflag |= C.tcflag_t(C.CS8)
		raw.c_cc[C.VMIN] = C.cc_t(1)
		raw.c_cc[C.VTIME] = C.cc_t(0)
		if _, err := C.tcsetattr(C.int(fd), C.TCSAFLUSH, &raw); err != nil {
			return fmt.Errorf("could not enable raw mode: %w", err)
		}
		return nil
	}
	restorer.restoreFn = func() error {
		if _, err := C.tcsetattr(C.int(fd), C.TCSAFLUSH, &saved); err != nil {
			return fmt.Errorf("could not restore terminal mode: %w", err)
		}
		return nil
	}

	return restorer, nil
}
