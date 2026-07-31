// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build darwin || linux

package main

import (
	"bufio"
	"fmt"
	"io"
)

const useExtractedWarningText = `
You have specified that rnodeconf should use a firmware extracted
from another device. Please note that this *only* works if you are
targeting a device of the same type that the firmware came from!

Flashing this firmware to a device it was not created for will most
likely result in it being inoperable until it is updated with the
correct firmware. Hit enter to continue.
`

func promptUseExtractedFirmware(out io.Writer, in io.Reader) error {
	if _, err := fmt.Fprint(out, useExtractedWarningText); err != nil {
		return err
	}
	reader := bufio.NewReader(in)
	_, err := reader.ReadString('\n')
	return err
}
