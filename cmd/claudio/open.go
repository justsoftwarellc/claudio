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

// cmdOpen implements `claudio open [<id>]`: resolve an instance the same
// way `claudio cd` does, then launch the configured editor against its
// worktree (ROD-130).
//
// This is `cd`'s sibling, and deliberately shares its resolution — the
// worktree being a plain host directory is the whole point of the
// host-owns-the-working-tree design (docs/architecture.md §5.1), and
// until now the CLI made the user copy that path into an editor by hand.
//
// The editor is required *at first use*, not at install: an unset editor
// is fine until this command runs, at which point it fails naming the
// exact command that fixes it. Requiring it earlier would break every
// existing install and every non-interactive run, none of which need an
// editor to create or attach to an instance.
func cmdOpen(ctx context.Context, args []string) int {
	for _, a := range args {
		if len(a) > 0 && a[0] == '-' {
			fmt.Fprintf(os.Stderr, "claudio open: unknown flag %q\n", a)
			return 1
		}
	}

	// Both editor checks run before the store is touched. They are the
	// command's own misconfiguration and have nothing to do with which
	// instance was named, so a user with a broken editor setting should
	// hear about that rather than about an unresolvable id or an
	// unreachable Docker — the first version resolved the instance first
	// and buried the editor error behind a store lookup.
	editor, err := configuredEditor()
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio open:", err)
		return 1
	}

	// Resolved separately from the "is one configured" check above so the
	// two failures read differently: "you have not set an editor" and
	// "the editor you set is not installed" have different fixes, and a
	// single "not found" would leave the user guessing which they hit.
	bin, err := exec.LookPath(editor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "claudio open: configured editor %q not found on PATH\n"+
			"  change it with: claudio config set editor <binary>\n", editor)
		return 1
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio open:", describeErr(err))
		return 1
	}
	defer c.Close()

	idOrName, ok := resolveIDWithClient(ctx, c, args, "claudio open")
	if !ok {
		return 1
	}

	inst, err := c.GetInstance(ctx, idOrName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio open:", describeErr(err))
		return 1
	}

	if err := launchEditor(bin, inst.WorktreeDir); err != nil {
		fmt.Fprintf(os.Stderr, "claudio open: launch %s: %v\n", editor, err)
		return 1
	}

	fmt.Printf("Opening %s in %s\n", inst.WorktreeDir, filepath.Base(bin))
	return 0
}

// errNoEditor is the unset case, phrased as the whole remedy. `claudio
// open` is usually the first command that needs an editor, so this is
// where most users learn the setting exists at all.
var errNoEditor = fmt.Errorf("no editor configured\n" +
	"  set one with: claudio config set editor <binary>   (e.g. code, cursor, nvim)")

// configuredEditor reads the editor from ~/.claudio/config.yml.
//
// Deliberately *not* falling back to $EDITOR: that variable means "the
// program that edits a commit message in this terminal", which for the
// very common `vi`/`nano` default would open a blocking terminal editor
// on a directory — not what someone typing `claudio open` wants. The
// installer offers $EDITOR as a suggested value instead, which keeps the
// convenience while leaving the choice explicit and inspectable.
func configuredEditor() (string, error) {
	dir, err := claudioStateDir()
	if err != nil {
		return "", err
	}
	global, err := config.LoadGlobalConfig(filepath.Join(dir, "config.yml"))
	if err != nil {
		return "", err
	}
	if global.Editor == "" {
		return "", errNoEditor
	}
	return global.Editor, nil
}

// launchEditor starts the editor detached and does not wait for it.
//
// Detached because a GUI editor's launcher process outlives the shell
// that spawned it: waiting would block the terminal for as long as the
// editor is open, and killing the process group on exit would close the
// user's editor when they close the terminal. Release() drops the
// process without reaping it, leaving it parented to init.
//
// Stdio is deliberately not inherited — a GUI editor writes launcher
// diagnostics to stderr that would interleave with the shell prompt
// after this process has already exited.
func launchEditor(bin, dir string) error {
	cmd := exec.Command(bin, dir)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
