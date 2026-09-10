// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/rodrigomorales/claudio/internal/config"
)

// cmdConfigSet implements `claudio config set <key> <value>`. Only
// `editor` exists so far (ROD-130), but the command is shaped as a
// key/value setter rather than a bare `claudio config set-editor` so the
// next machine-level setting lands as a key instead of a new verb — the
// same reasoning cmdConfig's doc gives for namespacing in the first
// place.
func cmdConfigSet(ctx context.Context, args []string) int {
	// Deliberately no flag scan before the arity check below. A value can
	// legitimately begin with '-' (an editor flag someone tried to pass),
	// and rejecting it as an unknown flag here would preempt the specific
	// "arguments are not supported" message that actually explains the
	// problem — the first version of this did exactly that.
	rest := args

	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "claudio config set: expected a key\n\nUsage:\n  claudio config set editor <binary>")
		return 1
	}
	if rest[0] != "editor" {
		fmt.Fprintf(os.Stderr, "claudio config set: unknown key %q (known keys: editor)\n", rest[0])
		return 1
	}
	if len(rest) < 2 {
		fmt.Fprintln(os.Stderr, "claudio config set: expected a value\n\nUsage:\n  claudio config set editor <binary>   (e.g. code, cursor, nvim)")
		return 1
	}
	if len(rest) > 2 {
		// A command string would arrive here as several arguments. Say so
		// precisely: `claudio config set editor code -n` is a reasonable
		// thing to try, and "too many arguments" alone would not explain
		// why the flag it accepts everywhere else is refused here.
		fmt.Fprintf(os.Stderr, "claudio config set editor: expected one binary, got %d arguments (%v)\n"+
			"  the editor is a bare binary name or path; arguments are not supported\n", len(rest)-1, rest[1:])
		return 1
	}

	editor := rest[1]

	// Validate before writing. A typo'd binary stored now would only
	// surface at the next `claudio open`, by which point the connection
	// to this command is lost — and the fix (re-running this) is the same
	// either way, so there is no reason to defer the error.
	if _, err := exec.LookPath(editor); err != nil {
		fmt.Fprintf(os.Stderr, "claudio config set editor: %q was not found on PATH\n"+
			"  pass a binary that exists (e.g. code, cursor, nvim), or an absolute path\n", editor)
		return 1
	}

	path, err := globalConfigPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio config set:", err)
		return 1
	}
	if err := config.SetGlobalEditor(path, editor); err != nil {
		fmt.Fprintln(os.Stderr, "claudio config set:", err)
		return 1
	}

	fmt.Printf("editor = %s\n%s\n", editor, path)
	return 0
}

// cmdConfigGet implements `claudio config get <key>`.
//
// The value goes to stdout alone so `$(claudio config get editor)` is
// usable; everything else, including the "not set" case, goes to stderr.
// That mirrors what cmdCd's doc establishes for command substitution.
func cmdConfigGet(ctx context.Context, args []string) int {
	var rest []string
	for _, a := range args {
		if len(a) > 0 && a[0] == '-' {
			fmt.Fprintf(os.Stderr, "claudio config get: unknown flag %q\n", a)
			return 1
		}
		rest = append(rest, a)
	}

	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "claudio config get: expected exactly one key\n\nUsage:\n  claudio config get editor")
		return 1
	}
	if rest[0] != "editor" {
		fmt.Fprintf(os.Stderr, "claudio config get: unknown key %q (known keys: editor)\n", rest[0])
		return 1
	}

	path, err := globalConfigPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio config get:", err)
		return 1
	}
	global, err := config.LoadGlobalConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio config get:", err)
		return 1
	}
	if global.Editor == "" {
		fmt.Fprintln(os.Stderr, "claudio config get: no editor configured\n"+
			"  set one with: claudio config set editor <binary>")
		return 1
	}

	fmt.Println(global.Editor)
	return 0
}

// globalConfigPath is where the machine-level config lives, honouring
// CLAUDIO_HOME the same way claudioStateDir does — so a test or a second
// isolated Claudio state writes its own config.yml rather than the
// operator's real one.
func globalConfigPath() (string, error) {
	dir, err := claudioStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yml"), nil
}
