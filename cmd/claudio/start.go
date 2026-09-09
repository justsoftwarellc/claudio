package main

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// cmdStart implements `claudio start <id> [--fresh]`. --fresh wipes the
// instance's home/ before recreating the container, discarding the
// existing Claude Code session instead of resuming it (the default —
// see docs/architecture.md §4.1/ROD-99).
func cmdStart(ctx context.Context, args []string) int {
	rest, fresh, ok := parseIDAndFresh(args, "claudio start")
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

	idOrName, ok := resolveIDWithClient(ctx, c, rest, "claudio start")
	if !ok {
		return 1
	}

	result, err := c.Start(ctx, idOrName, fresh, env, terminalProgress())
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio start:", describeErr(err))
		return 1
	}

	fmt.Printf("Started %s\n", result.InstanceID)
	fmt.Printf("Attach with: claudio attach %s\n", result.InstanceID)
	return 0
}

// parseIDAndFresh splits the common `[<id>] [--fresh]` shape shared by
// start, restart and rebuild into the positional arguments (left for
// resolveIDWithClient, since the id is optional — ROD-117) and the
// --fresh flag. Prints its own error message (naming caller) and returns
// ok=false on an unknown flag, so callers can just return 1 without
// duplicating the message.
func parseIDAndFresh(args []string, caller string) (rest []string, fresh bool, ok bool) {
	for _, a := range args {
		if a == "--fresh" {
			fresh = true
			continue
		}
		if strings.HasPrefix(a, "-") {
			fmt.Fprintf(os.Stderr, "%s: unknown flag %q\n", caller, a)
			return nil, false, false
		}
		rest = append(rest, a)
	}
	return rest, fresh, true
}
