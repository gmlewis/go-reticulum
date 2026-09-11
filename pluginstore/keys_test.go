// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package pluginstore

import "testing"

// TestValidKeyControlChars pins the key sanitation: control characters
// (including ANSI escapes) and DEL are rejected so a hostile key can never
// become a terminal-hostile filename.
func TestValidKeyControlChars(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"a\x01b", "\x1b[31m", "a\x7fb", "\r", "\n"} {
		if validKey(key) {
			t.Errorf("validKey(%q) = true, want false (control characters rejected)", key)
		}
	}
	for _, key := range []string{"greeting", "counter-1", "user_2026.name"} {
		if !validKey(key) {
			t.Errorf("validKey(%q) = false, want true", key)
		}
	}
}
