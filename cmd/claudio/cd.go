package main

import (
	"context"
	"fmt"
	"os"
)

// cmdCd implements `claudio cd <id>`: prints the instance's worktree
// path so a shell wrapper can `cd` into it — see docs/architecture.md
// §9's suggested `cdc() { cd "$(claudio cd "$1")"; }`. Only the path
// goes to stdout; everything else (including errors) goes to stderr, so
// command substitution in the wrapper never captures anything but the
// path.
func cmdCd(ctx context.Context, args []string) int {
	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio cd:", describeErr(err))
		return 1
	}
	defer c.Close()

	idOrName, ok := resolveIDWithClient(ctx, c, args, "claudio cd")
	if !ok {
		return 1
	}

	inst, err := c.GetInstance(ctx, idOrName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio cd:", describeErr(err))
		return 1
	}

	fmt.Println(inst.WorktreeDir)
	return 0
}
