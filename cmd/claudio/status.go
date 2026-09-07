package main

import (
	"context"
	"fmt"
	"os"
)

// cmdStatus implements `claudio status <id>`: the single-instance detail
// view — ls's ID/NAME/BRANCH/STATUS/PORTS row plus the fields ls omits
// for space (repo URL, workspace path, container ID).
func cmdStatus(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: claudio status <id>")
		return 1
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio status:", err)
		return 1
	}
	defer c.Close()

	inst, err := c.Status(ctx, args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio status:", err)
		return 1
	}

	fmt.Printf("ID:        %s\n", inst.ID)
	fmt.Printf("Name:      %s\n", displayName(inst))
	fmt.Printf("Repo:      %s\n", inst.RepoURL)
	fmt.Printf("Branch:    %s\n", inst.Branch)
	fmt.Printf("Status:    %s\n", statusOf(inst))
	fmt.Printf("Workspace: %s\n", inst.WorktreeDir)
	if inst.ContainerID != nil {
		fmt.Printf("Container: %s\n", shortID(*inst.ContainerID))
	}
	fmt.Printf("Ports:     %s\n", portsOf(inst))
	return 0
}
