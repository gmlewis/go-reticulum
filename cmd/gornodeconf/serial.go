// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

const rnodeBaudRate = 115200

type serialPort interface {
	io.Reader
	io.Writer
	Close() error
}

type serialSettings struct {
	Port             string
	BaudRate         int
	ByteSize         int
	Parity           string
	StopBits         int
	XonXoff          bool
	RTSCTS           bool
	Timeout          time.Duration
	InterByteTimeout *time.Duration
	WriteTimeout     *time.Duration
	DSRDTR           bool
}

type serialOpener func(serialSettings) (serialPort, error)

type cliRuntime struct {
	openSerial   serialOpener
	discoverPort func() (string, []string, error)
	stdin        io.Reader
	debug        bool
}

func newRuntime() cliRuntime {
	return cliRuntime{
		openSerial: defaultOpenSerial,
		stdin:      os.Stdin,
	}
}

func (rt cliRuntime) rnodeOpenSerial(port string) (serialPort, error) {
	opener := rt.openSerial
	if opener == nil {
		opener = defaultOpenSerial
	}
	serial, err := opener(serialSettings{
		Port:             port,
		BaudRate:         rnodeBaudRate,
		ByteSize:         8,
		Parity:           "N",
		StopBits:         1,
		XonXoff:          false,
		RTSCTS:           false,
		Timeout:          0,
		DSRDTR:           false,
		InterByteTimeout: nil,
		WriteTimeout:     nil,
	})
	if err != nil {
		return nil, err
	}
	if rt.debug {
		log.Printf("gornodeconf debug open %v", port)
		return &debugSerial{name: port, serialPort: serial}, nil
	}
	return serial, nil
}

func rnodeOpenSerial(port string) (serialPort, error) {
	return newRuntime().rnodeOpenSerial(port)
}

type debugSerial struct {
	name string
	serialPort
}

func (s *debugSerial) Read(data []byte) (int, error) {
	n, err := s.serialPort.Read(data)
	if n > 0 {
		log.Printf("gornodeconf debug read %v %v", s.name, fmt.Sprintf("%v", append([]byte(nil), data[:n]...)))
	}
	if err != nil {
		log.Printf("gornodeconf debug read error %v %v", s.name, err)
	}
	return n, err
}

func (s *debugSerial) Write(data []byte) (int, error) {
	log.Printf("gornodeconf debug write %v %v", s.name, fmt.Sprintf("%v", append([]byte(nil), data...)))
	n, err := s.serialPort.Write(data)
	if err != nil {
		log.Printf("gornodeconf debug write error %v %v", s.name, err)
	}
	return n, err
}

func (s *debugSerial) Close() error {
	log.Printf("gornodeconf debug close %v", s.name)
	return s.serialPort.Close()
}
