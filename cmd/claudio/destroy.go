package main

import (
	"context"
	"fmt"
	"os"

	"github.com/rodrigomorales/claudio/internal/core"
)

// cmdDestroy implements `claudio destroy <id> [--keep-workspace]`.
func cmdDestroy(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: claudio destroy <id> [--keep-workspace]")
		return 1
	}

	idOrName := args[0]
	keepWorkspace := false
	for _, a := range args[1:] {
		switch a {
		case "--keep-workspace":
			keepWorkspace = true
		default:
			fmt.Fprintf(os.Stderr, "claudio destroy: unknown flag %q\n", a)
			return 1
		}
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio destroy:", describeErr(err))
		return 1
	}
	defer c.Close()

	if err := c.Destroy(ctx, core.DestroyParams{IDOrName: idOrName, KeepWorkspace: keepWorkspace}); err != nil {
		fmt.Fprintln(os.Stderr, "claudio destroy:", describeErr(err))
		return 1
	}

	fmt.Printf("Destroyed %s\n", idOrName)
	return 0
}
