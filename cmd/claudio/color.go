// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"os"

	"golang.org/x/term"
)

// ANSI codes for the few states worth calling out visually in `ls`/
// `status` output — docs/architecture.md §9's "Respect NO_COLOR and
// non-TTY stdout." Kept to a small, fixed set rather than a general
// styling library: this project has exactly two states that benefit
// from color (an OOM kill is a failure worth a human's eye; a stopped
// instance is inert and can recede) and nothing else does yet.
const (
	ansiRed   = "\x1b[31m"
	ansiFaint = "\x1b[2m"
	ansiReset = "\x1b[0m"
)

// colorEnabled reports whether it's safe to write ANSI escapes to
// stdout: the NO_COLOR convention (https://no-color.org, referenced
// directly by docs/architecture.md §9) — any non-empty value disables
// color regardless of content — takes precedence over the TTY check, so
// a user's explicit preference always wins over what stdout happens to
// be connected to. Absent NO_COLOR, color is on only when stdout is
// actually a terminal — piping into `less` or a file must never embed
// escape codes a human isn't there to interpret.
func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// colorize wraps s in code if color is enabled, otherwise returns s
// unchanged — the one call every color use site makes, so none of them
// duplicate the NO_COLOR/TTY check themselves.
func colorize(code, s string) string {
	if !colorEnabled() {
		return s
	}
	return code + s + ansiReset
}
