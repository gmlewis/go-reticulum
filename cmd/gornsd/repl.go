// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
)

type replT struct {
	ret    *rns.Reticulum
	logger *rns.Logger
	in     io.Reader
	out    io.Writer
}

func newREPL(ret *rns.Reticulum, logger *rns.Logger, in io.Reader, out io.Writer) *replT {
	return &replT{
		ret:    ret,
		logger: logger,
		in:     in,
		out:    out,
	}
}

func (r *replT) cmdHelp() string {
	return strings.TrimSpace(`
help        show this help text
version     show the gornsd version
status      show Reticulum instance status
interfaces  list configured interfaces
loglevel    show or change the logger level
quit        exit the REPL
exit        exit the REPL
`)
}

func (r *replT) cmdVersion() string {
	return "gornsd " + rns.VERSION
}

func (r *replT) cmdStatus() string {
	if r == nil || r.ret == nil {
		return "(no reticulum instance)"
	}
	switch {
	case r.ret.IsConnectedToSharedInstance():
		return "connected to a shared instance"
	case r.ret.IsSharedInstance():
		return "shared instance"
	case r.ret.IsStandaloneInstance():
		return "standalone instance"
	default:
		return "unknown reticulum instance state"
	}
}

func (r *replT) cmdInterfaces() string {
	if r == nil || r.ret == nil || r.ret.Transport() == nil {
		return "(no interfaces)"
	}
	ifaces := r.ret.Transport().GetInterfaces()
	if len(ifaces) == 0 {
		return "(no interfaces)"
	}

	var builder strings.Builder
	for index, iface := range ifaces {
		if index > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(fmt.Sprintf("%v (%v) status=%v", iface.Name(), iface.Type(), iface.Status()))
	}
	return builder.String()
}

func (r *replT) cmdLogLevel(args []string) string {
	if r == nil || r.logger == nil {
		return "(no logger)"
	}
	if len(args) == 0 {
		level := r.logger.GetLogLevel()
		return fmt.Sprintf("%v %v", level, strings.TrimSpace(rns.LogLevelName(level)))
	}
	if len(args) != 1 {
		return "usage: loglevel [0-7]"
	}
	level, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Sprintf("invalid log level %q", args[0])
	}
	if level < 0 || level > 7 {
		return fmt.Sprintf("invalid log level %q", args[0])
	}
	r.logger.SetLogLevel(level)
	return fmt.Sprintf("log level set to %v %v", level, strings.TrimSpace(rns.LogLevelName(level)))
}

func (r *replT) dispatch(line string) (output string, done bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false
	}

	parts := strings.Fields(line)
	command := strings.ToLower(parts[0])
	args := parts[1:]

	switch command {
	case "help":
		return r.cmdHelp(), false
	case "version":
		return r.cmdVersion(), false
	case "status":
		return r.cmdStatus(), false
	case "interfaces":
		return r.cmdInterfaces(), false
	case "loglevel":
		return r.cmdLogLevel(args), false
	case "quit", "exit":
		return "Goodbye.", true
	default:
		return fmt.Sprintf("unknown command: %v", command), false
	}
}

func (r *replT) Run(ctx context.Context) {
	if r == nil || r.in == nil || r.out == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if _, err := fmt.Fprintf(r.out, "gornsd %v\n", rns.VERSION); err != nil {
		return
	}

	lines := make(chan string)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(r.in)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			if _, err := fmt.Fprint(r.out, ">>> "); err != nil {
				return
			}
			output, done := r.dispatch(line)
			if output != "" {
				if _, err := fmt.Fprintln(r.out, output); err != nil {
					return
				}
			}
			if done {
				return
			}
		}
	}
}
