// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package utils

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// androidGetpropPath is the absolute path to Android's system-property reader.
//
// Absolute and not a bare name on purpose: Android's seccomp policy kills a
// process on the faccessat2(2) that Go's exec.LookPath issues while resolving an
// unqualified name, so a bare program name is a crash there rather than a style
// choice.
const androidGetpropPath = "/system/bin/getprop"

// androidZoneProperty is the Android system property that names the device's time
// zone, e.g. "America/New_York".
const androidZoneProperty = "persist.sys.timezone"

// propertyReadTimeout bounds the property read. A property read that hangs must
// not hang the interface or daemon that is starting up behind it.
const propertyReadTimeout = 2 * time.Second

// UseSystemZone points time.Local at the time zone the operating system is set to,
// and returns the name of the zone it used. It returns "" when it left time.Local
// alone.
//
// The Go runtime works the local zone out from $TZ and from the zoneinfo files a
// Unix system keeps in /etc and /usr/share. An Android device has neither: it
// resolves its zone from a system property, and carries the zone data packed in a
// format only bionic reads. A Go program running there therefore gets
// time.Local == time.UTC and prints every time as UTC — a whole time zone's
// worth of wrongness in a chat log or a daemon log.
//
// Nothing is changed when the device does not answer with a zone name or when that
// name cannot be loaded. A program that leaves the zone alone prints UTC, which is
// at least a zone somebody can convert; a program that guesses would be wrong in a
// way nobody can.
//
// The caller decides whether a lookup by name can succeed on its platform: a
// binary that links the embedded database
//
//	import _ "time/tzdata"
//
// resolves any zone name on a device with no zoneinfo files at all.
func UseSystemZone() string {
	name, loc, ok := resolveZone(readAndroidProperty, time.LoadLocation)
	if !ok {
		return ""
	}
	time.Local = loc
	return name
}

// resolveZone returns the zone the device reports, in the two steps that fail
// independently: reading the name, and loading the rules that name stands for.
func resolveZone(property func(string) (string, error), load func(string) (*time.Location, error)) (string, *time.Location, bool) {
	reported, err := property(androidZoneProperty)
	if err != nil {
		return "", nil, false
	}
	name := strings.TrimSpace(reported)
	if !usableZoneName(name) {
		return "", nil, false
	}
	loc, err := load(name)
	if err != nil {
		return "", nil, false
	}
	return name, loc, true
}

// usableZoneName reports whether a name a system property handed over is one to
// look up.
//
// The property is external input, so it is never treated as a path: an absolute
// path or one that walks upwards names a file this has no business reading, and a
// lookup of it would be a file read chosen by whoever set the property. "UTC" is
// refused because a device that is already on UTC has nothing to be improved.
func usableZoneName(name string) bool {
	if name == "" || name == "UTC" || name == "Local" {
		return false
	}
	if strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return false
	}
	return !strings.ContainsAny(name, " \t\n\x00")
}

// readAndroidProperty reads one Android system property, or reports why it could
// not. It is the real [resolveZone] property reader; on a system without the tool
// the error is what leaves the zone alone.
func readAndroidProperty(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), propertyReadTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, androidGetpropPath, name).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
