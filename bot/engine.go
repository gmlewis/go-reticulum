// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"context"
	"errors"
	"strings"
)

// Engine is the bot's command surface as a first-class library value: it owns
// the command registry, the live sensors behind it, and nothing else. It opens
// no socket, dials no hub, and starts no goroutine, which is what makes it the
// zero-hop evaluator: Engine.Eval produces the same answer the radio would
// carry, computed entirely in-process.
//
// An Engine is what a field appliance (cmd/grl) runs when no hub is reachable,
// and it is also what the captive portal drives, so the radio answer and the
// dashboard answer can never disagree: both read one registry over one pair of
// sensors.
type Engine struct {
	reg *registry
	b   *bot
}

// NewEngine builds an in-process command engine over the given configuration,
// paths, and live sensors. A nil gps and a nil compass are both normal: every
// position-aware command then reports that it does not know where the device
// is, which is the truth, rather than failing.
//
// The caller owns the sensors and the paths; the engine only reads them.
func NewEngine(cfg *BotConfig, paths BotPaths, gps *GPSReader, compass *CompassReader) *Engine {
	b := newBot(cfg, paths, nil, nil, nil, botHooks{})
	reg := newRegistry(b)
	reg.gps = gps
	reg.compass = compass
	return &Engine{reg: reg, b: b}
}

// Eval evaluates one command line and returns its answer. cmd is the command
// word without a prefix ("whereami", "med", "sun"), and args are the words
// after it, joined with single spaces exactly as a chat client would send them.
//
// The answer is the command's reply with its lines joined by newlines, which is
// the whole reply a radio user would read; an error is returned only when ctx
// is already cancelled, because a command that cannot answer says so in its
// own text rather than failing.
func (e *Engine) Eval(ctx context.Context, cmd string, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if e == nil || e.reg == nil {
		return "", errors.New("bot: no engine")
	}
	line := strings.TrimSpace(strings.Join(append([]string{cmd}, args...), " "))
	return strings.Join(e.reg.RunLocal(line), "\n"), nil
}

// RunLocal evaluates one whole command line, exactly as the captive portal
// sends it. It is the LocalRunner the portal needs, and a line that needs a
// live hub link answers with that limitation instead of failing.
func (e *Engine) RunLocal(line string) []string {
	if e == nil || e.reg == nil {
		return []string{"no command engine is available"}
	}
	return e.reg.RunLocal(line)
}

// Commands returns every command name the engine answers, in registry order.
func (e *Engine) Commands() []string {
	if e == nil || e.reg == nil {
		return nil
	}
	return e.reg.names()
}

// OfflineCommands returns the command names the engine can answer with no hub
// link behind it, in registry order. It is the list the offline portal offers.
func (e *Engine) OfflineCommands() []string {
	if e == nil || e.reg == nil {
		return nil
	}
	return e.reg.localNames()
}

// Config returns the configuration the engine was built with. It is nil when
// the engine was built without one.
func (e *Engine) Config() *BotConfig {
	if e == nil || e.reg == nil {
		return nil
	}
	return e.reg.config()
}

// currentHeading reports the live compass heading. The portal reads it through
// the headingSource interface, so an Engine gives the dashboard the same
// corrected heading the commands use.
func (e *Engine) currentHeading() (CompassHeading, bool) {
	if e == nil || e.reg == nil {
		return CompassHeading{}, false
	}
	return e.reg.currentHeading()
}

// activeBeacons reports the distress beacons the compass rose vectors toward.
// The portal reads it through the beaconSource interface.
func (e *Engine) activeBeacons() []SOSRecord {
	if e == nil || e.reg == nil {
		return nil
	}
	return e.reg.activeBeacons()
}

// OpenGPS builds the GNSS reader a configuration asks for: a streaming reader
// over GPSPort when one is configured, a static provider holding GPSFix when no
// port is, and nil when neither is set. The caller owns the returned reader and
// must Close it.
func OpenGPS(cfg *BotConfig) (*GPSReader, error) {
	return openGPS(cfg)
}

// OpenCompass builds the electronic-compass reader a configuration asks for: a
// streaming reader over CompassPort when one is configured, a static provider
// holding CompassHeading when no port is, and nil when neither is set. The
// caller owns the returned reader and must Close it.
func OpenCompass(cfg *BotConfig) (*CompassReader, error) {
	return openCompass(cfg)
}
