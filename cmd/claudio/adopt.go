package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rodrigomorales/claudio/internal/core"
)

// cmdAdopt implements `claudio adopt <container>` (ROD-99): reconstructs
// a store row for an untracked container from its Docker labels. Accepts
// a container ID or name (or unambiguous prefix), matched the same way
// `claudio ls`'s UNTRACKED section displays them.
func cmdAdopt(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: claudio adopt <container>")
		return 1
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio adopt:", describeErr(err))
		return 1
	}
	defer c.Close()

	target, err := resolveUntracked(ctx, c, args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio adopt:", describeErr(err))
		return 1
	}

	instanceID, err := c.Adopt(ctx, target.ContainerID, target.CreatedAt)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio adopt:", describeErr(err))
		return 1
	}

	fmt.Printf("Adopted %s as instance %s\n", shortID(target.ContainerID), instanceID)
	fmt.Println("Note: repo_root and worktree_dir are unknown from Docker labels alone — `claudio cd` will not work for this instance until fixed manually.")
	return 0
}

// cmdForget implements `claudio forget <container>` (ROD-99): removes an
// untracked container permanently, without reconstructing a store row.
func cmdForget(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: claudio forget <container>")
		return 1
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio forget:", describeErr(err))
		return 1
	}
	defer c.Close()

	target, err := resolveUntracked(ctx, c, args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio forget:", describeErr(err))
		return 1
	}

	if err := c.Forget(ctx, target.ContainerID); err != nil {
		fmt.Fprintln(os.Stderr, "claudio forget:", describeErr(err))
		return 1
	}

	fmt.Printf("Forgot (removed) %s\n", shortID(target.ContainerID))
	return 0
}

// resolveUntracked matches ref against the untracked containers ls would
// show, by container ID, container ID prefix, or exact container name —
// the same identifiers docs/architecture.md §9 promises work for other
// commands. Ambiguous prefixes and no-match are both reported by name so
// the user can see exactly what `claudio ls` would show.
func resolveUntracked(ctx context.Context, c interface {
	ListInstances(ctx context.Context) ([]core.InstanceView, []core.UntrackedContainer, error)
}, ref string) (core.UntrackedContainer, error) {
	_, untracked, err := c.ListInstances(ctx)
	if err != nil {
		return core.UntrackedContainer{}, err
	}

	var matches []core.UntrackedContainer
	for _, u := range untracked {
		if u.ContainerID == ref || u.ContainerName == ref || strings.HasPrefix(u.ContainerID, ref) {
			matches = append(matches, u)
		}
	}

	switch len(matches) {
	case 0:
		return core.UntrackedContainer{}, fmt.Errorf("no untracked container matches %q — see `claudio ls`", ref)
	case 1:
		return matches[0], nil
	default:
		return core.UntrackedContainer{}, fmt.Errorf("%q matches %d untracked containers — use a longer prefix", ref, len(matches))
	}
}
