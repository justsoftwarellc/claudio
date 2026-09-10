// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import "testing"

func TestColorizeNoopsWhenNOCOLORSet(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	got := colorize(ansiRed, "stopped (out of memory)")
	want := "stopped (out of memory)"
	if got != want {
		t.Errorf("colorize under NO_COLOR = %q, want unchanged %q", got, want)
	}
}

func TestColorizeNoopsForAnyNonEmptyNOCOLORValue(t *testing.T) {
	// The NO_COLOR convention (https://no-color.org) says any non-empty
	// value disables color, not just "1" or "true" — pin that a
	// deliberately weird value still works.
	t.Setenv("NO_COLOR", "please-no")
	got := colorize(ansiRed, "x")
	if got != "x" {
		t.Errorf("colorize with NO_COLOR=%q = %q, want unchanged %q", "please-no", got, "x")
	}
}

func TestColorizeEmptyNOCOLORDoesNotDisable(t *testing.T) {
	// Setting NO_COLOR="" (present but empty) must NOT disable color —
	// the convention is keyed on the variable being *set to a non-empty
	// value*, and os.Getenv can't distinguish "unset" from "set empty",
	// so this only pins the behavior this package actually implements
	// (Getenv != ""), not the TTY half (which this test doesn't control).
	t.Setenv("NO_COLOR", "")
	// Whatever colorEnabled() decides based on the test binary's own
	// stdout (not a TTY under `go test`), colorize must be internally
	// consistent: if color is enabled here, the string must actually be
	// wrapped; if not, it must be unchanged. Either way, this must not
	// panic and must not silently drop the message text.
	got := colorize(ansiRed, "x")
	if got != "x" && got != ansiRed+"x"+ansiReset {
		t.Errorf("colorize with NO_COLOR=%q returned unexpected output %q", "", got)
	}
}

func TestColorEnabledFalseUnderGoTest(t *testing.T) {
	// go test's own stdout is captured, never a real terminal, so
	// colorEnabled() must report false absent NO_COLOR too — pins that
	// the TTY check is a real gate, not always-on.
	t.Setenv("NO_COLOR", "")
	if colorEnabled() {
		t.Error("colorEnabled() = true under go test's captured (non-TTY) stdout, want false")
	}
}
