// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bufio"
	"fmt"
	"io"
)

func promptManualFlashEntry(out io.Writer, in io.Reader) error {
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "Please put the board into flashing mode now, by holding the BOOT or PRG button,"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "while momentarily pressing the RESET button. Then release the BOOT or PRG button."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "Hit enter when this is done."); err != nil {
		return err
	}
	_, err := bufio.NewReader(in).ReadString('\n')
	return err
}

func promptManualFlashExit(out io.Writer, in io.Reader) error {
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "Please take the board out of flashing mode by momentarily pressing the RESET button."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "Hit enter when this is done."); err != nil {
		return err
	}
	_, err := bufio.NewReader(in).ReadString('\n')
	return err
}
