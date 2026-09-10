// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"context"
	"fmt"
)

// cmdDaemon implements `claudio daemon <subcommand>`. Phase 1 ships no
// daemon (docs/architecture.md §12.4, ROD-95) — claudiod is introduced
// in ROD-101 alongside the port proxy, which is the first thing that
// actually needs a long-lived listening process. This command exists now
// so the future `claudio daemon start|stop|status` surface is stable and
// so users get an honest answer instead of a missing command.
func cmdDaemon(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Println("Usage: claudio daemon status|start|stop")
		return 1
	}
	switch args[0] {
	case "status":
		fmt.Println("No daemon in this phase — claudio runs entirely as a CLI (see docs/architecture.md §12.4).")
		fmt.Println("The daemon arrives with runtime port discovery (ROD-101), which needs a long-lived listening socket.")
		return 0
	case "start", "stop":
		fmt.Printf("claudio daemon %s: not applicable — no daemon exists in this phase (ROD-101 introduces it).\n", args[0])
		return 1
	default:
		fmt.Printf("claudio daemon: unknown subcommand %q\n", args[0])
		return 1
	}
}
