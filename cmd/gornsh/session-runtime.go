// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

type messageSender interface {
	Send(msg rns.Message) (*rns.Envelope, error)
}

type activeCommand struct {
	mu       sync.Mutex
	stdin    io.WriteCloser
	kill     func() error
	closed   bool
	finished bool
}

var errStdinClosed = errors.New("stdin is closed")

func (c *activeCommand) writeStdin(data []byte, eof bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errStdinClosed
	}

	if len(data) > 0 {
		if _, err := c.stdin.Write(data); err != nil {
			return err
		}
	}

	if eof {
		c.closed = true
		return c.stdin.Close()
	}

	return nil
}

func (c *activeCommand) markFinished() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.finished = true
}

func (c *activeCommand) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	stdin := c.stdin
	shouldKill := !c.finished && c.kill != nil
	kill := c.kill
	c.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if shouldKill {
		_ = kill()
	}
}

func wireListenerChannelSession(link *rns.Link, opts options, allowedList [][]byte) {
	session := newListenerSession(listenerSessionConfig{
		AllowAll:           opts.noAuth,
		AllowRemoteCommand: !opts.noRemoteCmd,
		RemoteCmdAsArgs:    opts.remoteAsArgs,
		DefaultCommand:     opts.commandLine,
		SoftwareVersion:    "gornsh " + rns.Version,
	})

	channel := link.GetChannel()
	registerProtocolMessageTypes(channel)

	var commandMu sync.Mutex
	var command *activeCommand

	link.SetRemoteIdentifiedCallback(func(l *rns.Link, id *rns.Identity) {
		allowed := opts.noAuth
		if !allowed {
			allowed = identityAllowed(id.Hash, allowedList)
		}
		if err := session.onInitiatorIdentified(id.Hash, allowed); err != nil {
			sendProtocolError(channel, err.Error(), true)
		}
	})

	link.SetLinkClosedCallback(func(l *rns.Link) {
		session.markTeardown()
		commandMu.Lock()
		if command != nil {
			command.close()
			command = nil
		}
		commandMu.Unlock()
	})

	channel.AddMessageHandler(func(msg rns.Message) bool {
		if session.isTeardown() {
			return true
		}

		switch typed := msg.(type) {
		case *versionInfoMessage:
			response, err := session.handleVersion(*typed)
			if err != nil {
				sendProtocolError(channel, err.Error(), true)
				return true
			}
			if response != nil {
				if err := sendMessageWithRetry(channel, response, time.Now().Add(2*time.Second)); err != nil {
					rns.Logf("Failed to send version info response: %v", rns.LogWarning, false, err)
				}
			}
			return true
		case *executeCommandMessage:
			cmdline, err := session.handleExecute(*typed)
			if err != nil {
				sendProtocolError(channel, err.Error(), true)
				return true
			}
			commandMu.Lock()
			if command != nil {
				command.close()
				command = nil
			}
			started, err := startSessionCommand(channel, cmdline, link.GetRemoteIdentity(), typed)
			if err != nil {
				commandMu.Unlock()
				sendProtocolError(channel, normalizeCommandStartError(err), true)
				return true
			}
			command = started
			commandMu.Unlock()
			return true
		case *windowSizeMessage:
			if err := session.handleWindowSize(*typed); err != nil {
				sendProtocolError(channel, err.Error(), true)
				return true
			}
			return true
		case *streamDataMessage:
			if err := session.handleStreamData(*typed); err != nil {
				sendProtocolError(channel, err.Error(), true)
				return true
			}
			commandMu.Lock()
			active := command
			commandMu.Unlock()
			if active == nil {
				sendProtocolError(channel, "no active command for stdin stream", true)
				return true
			}
			if err := active.writeStdin(typed.Data, typed.EOF); err != nil {
				if errors.Is(err, errStdinClosed) || errors.Is(err, io.ErrClosedPipe) {
					return true
				}
				sendProtocolError(channel, err.Error(), true)
				return true
			}
			return true
		case *noopMessage:
			if session.isRunning() {
				if err := sendMessageWithRetry(channel, &noopMessage{}, time.Now().Add(2*time.Second)); err != nil {
					rns.Logf("Failed to echo noop message: %v", rns.LogWarning, false, err)
				}
			}
			return true
		default:
			return false
		}
	})
}

func normalizeCommandStartError(err error) string {
	if err == nil {
		return "command start failed"
	}
	return "command start failed: " + err.Error()
}

func startSessionCommand(sender messageSender, commandLine []string, remoteIdentity *rns.Identity, execute *executeCommandMessage) (*activeCommand, error) {
	if len(commandLine) == 0 {
		return nil, errors.New("no command to execute")
	}

	cmd := exec.Command(commandLine[0], commandLine[1:]...)
	cmd.Env = buildSessionCommandEnv(os.Environ(), remoteIdentity, execute)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	active := &activeCommand{
		stdin: stdinPipe,
		kill: func() error {
			if cmd.Process == nil {
				return nil
			}
			return cmd.Process.Kill()
		},
	}

	go streamPipe(sender, stdoutPipe, streamIDStdout)
	go streamPipe(sender, stderrPipe, streamIDStderr)

	go func() {
		err := cmd.Wait()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 127
				sendProtocolErrorToSender(sender, err.Error(), true)
			}
		}
		active.markFinished()
		_ = sendMessageWithRetry(sender, &commandExitedMessage{ReturnCode: exitCode}, time.Now().Add(2*time.Second))
		active.close()
	}()

	return active, nil
}

func buildSessionCommandEnv(base []string, remoteIdentity *rns.Identity, execute *executeCommandMessage) []string {
	env := append([]string{}, base...)
	if remoteIdentity != nil {
		env = upsertEnv(env, "RNS_REMOTE_IDENTITY", remoteIdentity.HexHash)
	}
	if execute != nil {
		if execute.Term != nil && *execute.Term != "" {
			env = upsertEnv(env, "TERM", *execute.Term)
		}
		if execute.Rows != nil && *execute.Rows > 0 {
			env = upsertEnv(env, "LINES", strconv.Itoa(*execute.Rows))
		}
		if execute.Cols != nil && *execute.Cols > 0 {
			env = upsertEnv(env, "COLUMNS", strconv.Itoa(*execute.Cols))
		}
	}
	return env
}

func upsertEnv(env []string, key, value string) []string {
	prefix := key + "="
	updated := make([]string, 0, len(env)+1)
	replaced := false

	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				updated = append(updated, prefix+value)
				replaced = true
			}
			continue
		}
		updated = append(updated, entry)
	}

	if !replaced {
		updated = append(updated, prefix+value)
	}

	return updated
}

func streamPipe(sender messageSender, reader io.ReadCloser, streamID int) {
	defer func() {
		if err := reader.Close(); err != nil {
			sendProtocolErrorToSender(sender, fmt.Sprintf("stream close failed: %v", err), false)
		}
	}()

	buf := make([]byte, 32*1024)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			_ = sendMessageWithRetry(sender, &streamDataMessage{StreamID: streamID, Data: chunk, EOF: false, Compressed: false}, time.Now().Add(2*time.Second))
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				sendProtocolErrorToSender(sender, err.Error(), true)
			}
			_ = sendMessageWithRetry(sender, &streamDataMessage{StreamID: streamID, Data: nil, EOF: true, Compressed: false}, time.Now().Add(2*time.Second))
			return
		}
	}
}

func sendProtocolError(channel *rns.Channel, message string, fatal bool) {
	err := sendMessageWithRetry(channel, &errorMessage{Message: message, Fatal: fatal, Data: nil}, time.Now().Add(2*time.Second))
	if err != nil {
		rns.Logf("Failed to send protocol error %q: %v", rns.LogWarning, false, message, err)
	}
}

func sendProtocolErrorToSender(sender messageSender, message string, fatal bool) {
	_ = sendMessageWithRetry(sender, &errorMessage{Message: message, Fatal: fatal, Data: nil}, time.Now().Add(2*time.Second))
}
