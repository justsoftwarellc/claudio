package main

import (
	"context"
	"fmt"
	"os"
)

// cmdStop implements `claudio stop <id>`.
func cmdStop(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: claudio stop <id>")
		return 1
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio stop:", describeErr(err))
		return 1
	}
	defer c.Close()

	if err := c.Stop(ctx, args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "claudio stop:", describeErr(err))
		return 1
	}

	fmt.Printf("Stopped %s\n", args[0])
	return 0
}
