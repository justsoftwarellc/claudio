// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// cmdConfigRestore implements `claudio config restore [<id>] [--json]`.
//
// The failure it repairs surfaces as an error from Claude Code, not from
// Claudio: `claudio attach` succeeds, the session comes up, and Claude
// Code refuses to start because ~/.claude.json is not valid JSON. Since
// the config is broken the session never becomes usable, so the fix
// cannot itself run inside the session — core.RestoreConfig rewrites the
// file on the host through the home/ bind mount, which is also why this
// works on a stopped instance.
//
// Output names every file it touched and prints the JSON syntax error
// from the old file when there was one: the user's next question after
// "it's fixed" is invariably "what was wrong with it", and the backup is
// only useful if they know it exists.
func cmdConfigRestore(ctx context.Context, args []string) int {
	var rest []string
	var asJSON bool
	for _, a := range args {
		if a == "--json" {
			asJSON = true
			continue
		}
		if strings.HasPrefix(a, "-") {
			fmt.Fprintf(os.Stderr, "claudio config restore: unknown flag %q\n", a)
			return 1
		}
		rest = append(rest, a)
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio config restore:", describeErr(err))
		return 1
	}
	defer c.Close()

	idOrName, ok := resolveIDWithClient(ctx, c, rest, "claudio config restore")
	if !ok {
		return 1
	}

	result, err := c.RestoreConfig(ctx, idOrName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio config restore:", describeErr(err))
		return 1
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			fmt.Fprintln(os.Stderr, "claudio config restore:", err)
			return 1
		}
		return 0
	}

	switch {
	case !result.Existed:
		fmt.Printf("  no config found; wrote a fresh one\n")
	case result.Parsed:
		fmt.Printf("  previous config parsed; reset the startup fields\n")
	default:
		fmt.Printf("  previous config was invalid JSON: %s\n", result.ParseError)
	}
	// Which file the content came back from matters: recovering Claude
	// Code's own backup restores the real session state, while falling
	// through to the baseline does not, and the user should know which
	// of the two they got.
	if result.RecoveredFrom != "" {
		fmt.Printf("  recovered  %s\n", result.RecoveredFrom)
	}
	if result.BackupPath != "" {
		fmt.Printf("  backed up  %s\n", result.BackupPath)
	}
	fmt.Printf("  wrote      %s\n", result.ConfigPath)
	fmt.Printf("  trusted    %s\n", result.Workdir)
	if len(result.SalvagedKeys) > 0 {
		fmt.Printf("  salvaged   %s\n", strings.Join(result.SalvagedKeys, ", "))
	}

	fmt.Printf("\nRestored config for %s\n", result.InstanceID)
	// The container reads ~/.claude.json once, when Claude Code starts,
	// so a session that is already up is still running against the old
	// file. Restart is what actually applies this — say so rather than
	// letting the user re-hit the same error and conclude the repair
	// didn't work.
	fmt.Printf("Apply it with: claudio restart %s\n", result.InstanceID)
	return 0
}

// cmdConfig dispatches `claudio config <subcommand>`. Namespaced rather
// than a flat `claudio config-restore` so later config operations
// (show, edit) land as siblings instead of new top-level verbs — the
// same shape `claudio image build` already established.
func cmdConfig(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "claudio config: expected a subcommand\n\nUsage:\n  claudio config restore [<id>] [--json]")
		return 1
	}
	switch args[0] {
	case "restore":
		return cmdConfigRestore(ctx, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "claudio config: unknown subcommand %q\n\nUsage:\n  claudio config restore [<id>] [--json]\n", args[0])
		return 1
	}
}
