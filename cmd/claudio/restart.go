package main

import (
	"context"
	"fmt"
	"os"
)

// cmdRestart implements `claudio restart <id> [--fresh]`: stop followed
// by start as one operation. Resumes the existing Claude Code session by
// default (home/ is preserved across the container swap); --fresh
// discards it instead — see docs/architecture.md §4.1/ROD-99, Q2.
func cmdRestart(ctx context.Context, args []string) int {
	idOrName, fresh, ok := parseIDAndFresh(args, "claudio restart")
	if !ok {
		return 1
	}

	env, ok := credentialEnv("claudio restart")
	if !ok {
		return 1
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio restart:", err)
		return 1
	}
	defer c.Close()

	result, err := c.Restart(ctx, idOrName, fresh, env)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio restart:", err)
		return 1
	}

	fmt.Printf("Restarted %s\n", result.InstanceID)
	fmt.Printf("Attach with: claudio attach %s\n", result.InstanceID)
	return 0
}
