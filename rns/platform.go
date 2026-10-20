// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import "strings"

// IsAndroid reports whether the host is Android, mirroring
// RNS.vendor.platformutils.is_android.
func IsAndroid() bool { return HostOS() == "android" }

// IsLinux reports whether the host is Linux, mirroring
// RNS.vendor.platformutils.is_linux.
func IsLinux() bool { return HostOS() == "linux" }

// IsDarwin reports whether the host is Darwin/macOS, mirroring
// RNS.vendor.platformutils.is_darwin.
func IsDarwin() bool { return HostOS() == "darwin" }

// IsWindows reports whether the host is Windows, mirroring
// RNS.vendor.platformutils.is_windows (a platform name starting with "win").
func IsWindows() bool { return strings.HasPrefix(HostOS(), "win") }

// UseEpoll reports whether epoll is the event mechanism, mirroring
// RNS.vendor.platformutils.use_epoll (Linux and Android).
func UseEpoll() bool { return IsLinux() || IsAndroid() }

// UseAFUnix reports whether AF_UNIX sockets are available, mirroring
// RNS.vendor.platformutils.use_af_unix (Linux and Android).
func UseAFUnix() bool { return IsLinux() || IsAndroid() }
