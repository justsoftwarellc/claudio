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

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio status:", describeErr(err))
		return 1
	}
	defer c.Close()

	idOrName, ok := resolveIDWithClient(ctx, c, args, "claudio status")
	if !ok {
		return 1
	}

	inst, err := c.Status(ctx, idOrName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio status:", describeErr(err))
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
	if inst.IsCompose() {
		fmt.Printf("Compose:   %s\n", *inst.ComposeProject)
		if len(inst.Sidecars) == 0 {
			fmt.Println("Sidecars:  (none reported)")
		} else {
			for i, s := range inst.Sidecars {
				label := "Sidecars:  "
				if i > 0 {
					label = "           "
				}
				state := s.State
				if s.Health != "" {
					state = fmt.Sprintf("%s (%s)", s.State, s.Health)
				}
				fmt.Printf("%s%s: %s\n", label, s.Service, state)
			}
		}
	}
	return 0
}
