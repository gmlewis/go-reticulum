// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// documentedCommandFiles are the two places a reader looks for the command
// surface: the README's command table and the bot's reference page.
var documentedCommandFiles = []string{
	filepath.Join("..", "README.md"),
	filepath.Join("..", "docs", "tools", "gorrcbot.md"),
}

// documentedCommands is the set of names a markdown file lists in the first cell
// of a table row, which is how both files present a command and its syntax.
func documentedCommands(text string) map[string]bool {
	out := make(map[string]bool)
	for line := range strings.SplitSeq(text, "\n") {
		name, ok := strings.CutPrefix(line, "| `")
		if !ok {
			continue
		}
		if end := strings.IndexAny(name, " `<>|"); end >= 0 {
			name = name[:end]
		}
		if name != "" {
			out[name] = true
		}
	}
	return out
}

// TestEveryCommandIsDocumented asserts the registry and the documentation name the
// same commands. A command that a refactor adds, renames, or drops and no document
// follows is invisible to a reader who has only the docs, which is how the last
// refactor left `kjv` out of the reference page and four commands out of the README
// table.
func TestEveryCommandIsDocumented(t *testing.T) {
	t.Parallel()

	reg, _, _ := commandFixture(t, nil)
	for _, path := range documentedCommandFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("could not read %v: %v", path, err)
		}
		documented := documentedCommands(string(data))
		for _, cmd := range reg.commands {
			if !documented[cmd.name] {
				t.Errorf("%v does not document the %v command", path, cmd.name)
			}
		}
	}
}

// TestUsageLinesNameTheirOwnCommand asserts a command's usage line begins with its
// own name. A usage line that names something else — an alias, or a sibling view of
// the same catalog — tells a reader to type a command they did not ask about, which
// is what `help alerts` did while its usage began with "wxalert" and what `help cell`
// did while its usage began with "tower".
func TestUsageLinesNameTheirOwnCommand(t *testing.T) {
	t.Parallel()

	reg, _, _ := commandFixture(t, nil)
	for _, cmd := range reg.commands {
		if cmd.usage == "" {
			continue
		}
		if name, _ := splitCommandLine(cmd.usage); name != cmd.name {
			t.Errorf("the %v command's usage line is %q, whose command word is %v",
				cmd.name, cmd.usage, name)
		}
	}
}
