package main

import (
	"context"
	"fmt"
	"os"
)

// cmdStart implements `claudio start <id> [--fresh]`. --fresh wipes the
// instance's home/ before recreating the container, discarding the
// existing Claude Code session instead of resuming it (the default —
// see docs/architecture.md §4.1/ROD-99).
func cmdStart(ctx context.Context, args []string) int {
	idOrName, fresh, ok := parseIDAndFresh(args, "claudio start")
	if !ok {
		return 1
	}

	env, ok := credentialEnv("claudio start")
	if !ok {
		return 1
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio start:", describeErr(err))
		return 1
	}
	defer c.Close()

	result, err := c.Start(ctx, idOrName, fresh, env, terminalProgress())
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio start:", describeErr(err))
		return 1
	}

	fmt.Printf("Started %s\n", result.InstanceID)
	fmt.Printf("Attach with: claudio attach %s\n", result.InstanceID)
	return 0
}

// parseIDAndFresh parses the common `<id> [--fresh]` shape shared by
// start and restart. Prints its own usage/error message (naming caller)
// and returns ok=false on any problem, so cmdStart/cmdRestart can just
// return 1 without duplicating the message.
func parseIDAndFresh(args []string, caller string) (idOrName string, fresh bool, ok bool) {
	if len(args) == 0 || len(args) > 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <id> [--fresh]\n", caller)
		return "", false, false
	}
	idOrName = args[0]
	if len(args) == 2 {
		if args[1] != "--fresh" {
			fmt.Fprintf(os.Stderr, "%s: unknown flag %q\n", caller, args[1])
			return "", false, false
		}
		fresh = true
	}
	return idOrName, fresh, true
}
