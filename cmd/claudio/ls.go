package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/rodrigomorales/claudio/internal/core"
)

// cmdLs implements `claudio ls`. Phase 1 has no attention/activity
// monitor yet (that's ROD-102, which needs hooks + the daemon), so the
// ATTENTION column is omitted for now rather than faked — see the
// project's stance on flagging assumptions instead of asserting them
// (docs/architecture.md, "to verify during implementation" notes).
func cmdLs(ctx context.Context, args []string) int {
	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio ls:", err)
		return 1
	}
	defer c.Close()

	instances, untracked, err := c.ListInstances(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio ls:", err)
		return 1
	}

	if len(instances) == 0 && len(untracked) == 0 {
		fmt.Println("No instances. Create one with `claudio create <repo>`.")
		return 0
	}

	if len(instances) > 0 {
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tBRANCH\tSTATUS\tPORTS")
		for _, inst := range instances {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				inst.ID, displayName(inst), inst.Branch, statusOf(inst), portsOf(inst))
		}
		tw.Flush()
	}

	if len(untracked) > 0 {
		fmt.Printf("\nUNTRACKED (%d) — carries Claudio labels but no matching instance record.\n", len(untracked))
		fmt.Println("Run `claudio adopt <container>` or `claudio forget <container>`.")
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "CONTAINER\tNAME\tREPO\tRUNNING")
		for _, u := range untracked {
			repo := u.RepoURL
			if repo == "" {
				repo = "—"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%t\n", shortID(u.ContainerID), u.ContainerName, repo, u.Running)
		}
		tw.Flush()
	}
	return 0
}

func displayName(inst core.InstanceView) string {
	if inst.Name != nil {
		return *inst.Name
	}
	return "—"
}

// statusOf reports what actually happened to the container, favoring
// Docker's live state over the store's desired_state — see
// docs/architecture.md §10.1 — and surfacing an OOM kill explicitly
// rather than a bare "stopped" (ROD-112: an unexplained stop is a worse
// failure mode than an explained one).
func statusOf(inst core.InstanceView) string {
	switch {
	case inst.OOMKilled:
		return "stopped (out of memory)"
	case inst.ContainerStatus != "":
		return inst.ContainerStatus
	default:
		return string(inst.DesiredState)
	}
}

func portsOf(inst core.InstanceView) string {
	if len(inst.Ports) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(inst.Ports))
	for _, p := range inst.Ports {
		parts = append(parts, fmt.Sprintf("%d", p.HostPort))
	}
	return strings.Join(parts, ",")
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
