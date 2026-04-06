// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
)

type appT struct {
	configDir string
	size      int
	probes    int
	timeout   float64
	wait      float64
	logger    *rns.Logger
	verbose   bool
	version   bool
	args      []string
}

var errHelp = errors.New("help requested")

func parseFlags(args []string, usageOutput io.Writer) (*appT, error) {
	app := &appT{size: DefaultProbeSize, probes: 1}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			app.args = append(app.args, args[i+1:]...)
			break
		}
		if arg == "-h" || arg == "--help" {
			app.usage(usageOutput)
			return nil, errHelp
		}
		if arg == "--version" {
			app.version = true
			continue
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			app.args = append(app.args, arg)
			continue
		}
		if strings.HasPrefix(arg, "--") {
			name := strings.TrimPrefix(arg, "--")
			switch name {
			case "config":
				value, next, err := consumeValue(args, i)
				if err != nil {
					return nil, err
				}
				app.configDir = value
				i = next
			case "size":
				value, next, err := consumeValue(args, i)
				if err != nil {
					return nil, err
				}
				n, err := strconv.Atoi(value)
				if err != nil {
					return nil, err
				}
				app.size = n
				i = next
			case "probes":
				value, next, err := consumeValue(args, i)
				if err != nil {
					return nil, err
				}
				n, err := strconv.Atoi(value)
				if err != nil {
					return nil, err
				}
				app.probes = n
				i = next
			case "timeout":
				value, next, err := consumeValue(args, i)
				if err != nil {
					return nil, err
				}
				n, err := strconv.ParseFloat(value, 64)
				if err != nil {
					return nil, err
				}
				app.timeout = n
				i = next
			case "wait":
				value, next, err := consumeValue(args, i)
				if err != nil {
					return nil, err
				}
				n, err := strconv.ParseFloat(value, 64)
				if err != nil {
					return nil, err
				}
				app.wait = n
				i = next
			case "verbose":
				app.verbose = true
			default:
				return nil, fmt.Errorf("flag provided but not defined: %v", arg)
			}
			continue
		}
		for pos := 1; pos < len(arg); pos++ {
			flagName := arg[pos]
			switch flagName {
			case 'v':
				app.verbose = true
			case 's', 'n', 't', 'w':
				if pos != len(arg)-1 {
					return nil, fmt.Errorf("flag needs an argument: -%c", flagName)
				}
				if i+1 >= len(args) {
					return nil, fmt.Errorf("flag needs an argument: -%c", flagName)
				}
				value := args[i+1]
				i++
				if err := applyShortFlag(app, flagName, value); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("flag provided but not defined: -%c", flagName)
			}
		}
	}
	return app, nil
}

func (a *appT) usage(w io.Writer) {
	_, _ = io.WriteString(w, usageText)
}

func applyShortFlag(app *appT, flagName byte, value string) error {
	switch flagName {
	case 's':
		n, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		app.size = n
	case 'n':
		n, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		app.probes = n
	case 't':
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return err
		}
		app.timeout = n
	case 'w':
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return err
		}
		app.wait = n
	default:
		return fmt.Errorf("flag provided but not defined: -%c", flagName)
	}
	return nil
}

func consumeValue(args []string, index int) (string, int, error) {
	if index+1 >= len(args) {
		return "", index, fmt.Errorf("flag needs an argument: %v", args[index])
	}
	return args[index+1], index + 1, nil
}

const usageText = `
usage: gornprobe [-h] [--config CONFIG] [-s SIZE] [-n PROBES] [-t seconds] [-w seconds] [--version] [-v]
                 [full_name] [destination_hash]

Go Reticulum Probe Utility

positional arguments:
  full_name             full destination name in dotted notation
  destination_hash      hexadecimal hash of the destination

options:
  -h, --help            show this help message and exit
  --config CONFIG       path to alternative Reticulum config directory
	-s SIZE, --size SIZE  size of probe packet payload in bytes
	-n PROBES, --probes PROBES
                        number of probes to send
  -t seconds, --timeout seconds
                        timeout before giving up
  -w seconds, --wait seconds
                        time between each probe
  --version             show program's version number and exit
  -v, --verbose
`
