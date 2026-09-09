package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rodrigomorales/claudio/internal/core"
)

// cmdDestroy implements `claudio destroy <id> [--keep-workspace]`.
func cmdDestroy(ctx context.Context, args []string) int {
	keepWorkspace := false
	var rest []string
	for _, a := range args {
		switch {
		case a == "--keep-workspace":
			keepWorkspace = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "claudio destroy: unknown flag %q\n", a)
			return 1
		default:
			rest = append(rest, a)
		}
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio destroy:", describeErr(err))
		return 1
	}
	defer c.Close()

	idOrName, ok := resolveIDWithClient(ctx, c, rest, "claudio destroy")
	if !ok {
		return 1
	}

	// Resolve to the canonical id before destroying: the pointer file
	// lists ids, so a destroy by --name must still remove the right line.
	instanceID := idOrName
	if inst, err := c.GetInstance(ctx, idOrName); err == nil {
		instanceID = inst.ID
	}

	if err := c.Destroy(ctx, core.DestroyParams{IDOrName: idOrName, KeepWorkspace: keepWorkspace}); err != nil {
		fmt.Fprintln(os.Stderr, "claudio destroy:", describeErr(err))
		return 1
	}

	forgetInstance(instanceID)

	fmt.Printf("Destroyed %s\n", idOrName)
	return 0
}
