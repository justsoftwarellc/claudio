package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/rodrigomorales/claudio/internal/core"
	"github.com/rodrigomorales/claudio/internal/store"
)

// cmdLs implements `claudio ls [--all] [--json]`. Phase 1 has no
// attention/activity monitor yet (that's ROD-102, which needs hooks +
// the daemon), so the ATTENTION column is omitted for now rather than
// faked — see the project's stance on flagging assumptions instead of
// asserting them (docs/architecture.md, "to verify during
// implementation" notes).
//
// Stopped instances are hidden unless --all, mirroring `docker ps` /
// `docker ps -a` — the mental model users already have for exactly this
// question. Destroyed instances never appear either way: `destroy`
// deletes the row outright (store.DeleteInstance), so there is nothing
// left to list. --json applies the same --all filtering as the human
// table — it is a format switch, not a second query — so a script
// piping `claudio ls --json` sees exactly what a human running `claudio
// ls` would, structured instead of tabular.
func cmdLs(ctx context.Context, args []string) int {
	showAll := false
	asJSON := false
	for _, arg := range args {
		switch arg {
		case "--all":
			showAll = true
		case "--json":
			asJSON = true
		default:
			fmt.Fprintf(os.Stderr, "claudio ls: unknown flag %q\n", arg)
			return 1
		}
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio ls:", describeErr(err))
		return 1
	}
	defer c.Close()

	instances, untracked, err := c.ListInstances(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio ls:", describeErr(err))
		return 1
	}

	hidden := 0
	if !showAll {
		visible := make([]core.InstanceView, 0, len(instances))
		for _, inst := range instances {
			if inst.DesiredState == store.StateStopped {
				hidden++
				continue
			}
			visible = append(visible, inst)
		}
		instances = visible
	}

	if asJSON {
		return printLsJSON(instances, untracked)
	}
	return printLsTable(instances, untracked, hidden)
}

// lsJSON is `claudio ls --json`'s exact wire shape — a caller scripting
// against this depends on these two field names, so they're fixed here
// rather than left to whatever encoding/json would default to for an
// anonymous struct at each call site. Both fields are always present
// (never omitted, even when empty) so a script doesn't need an extra
// nil-check for "no instances" versus "the key isn't there at all."
type lsJSON struct {
	Instances []core.InstanceView       `json:"instances"`
	Untracked []core.UntrackedContainer `json:"untracked"`
}

func printLsJSON(instances []core.InstanceView, untracked []core.UntrackedContainer) int {
	if instances == nil {
		instances = []core.InstanceView{}
	}
	if untracked == nil {
		untracked = []core.UntrackedContainer{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(lsJSON{Instances: instances, Untracked: untracked}); err != nil {
		fmt.Fprintln(os.Stderr, "claudio ls:", describeErr(err))
		return 1
	}
	return 0
}

func printLsTable(instances []core.InstanceView, untracked []core.UntrackedContainer, hidden int) int {
	if len(instances) == 0 && len(untracked) == 0 {
		if hidden > 0 {
			fmt.Printf("No running instances (%d stopped — see `claudio ls --all`).\n", hidden)
			return 0
		}
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

	if hidden > 0 {
		fmt.Printf("\n%d stopped instance(s) hidden — see `claudio ls --all`.\n", hidden)
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
// failure mode than an explained one). Colored per docs/architecture.md
// §9's "Respect NO_COLOR and non-TTY stdout" — an OOM kill is a failure
// worth a human's eye (red); an inert stopped instance can recede
// (faint). colorize itself no-ops under NO_COLOR or a non-TTY stdout.
func statusOf(inst core.InstanceView) string {
	switch {
	case inst.OOMKilled:
		return colorize(ansiRed, "stopped (out of memory)")
	case inst.ContainerStatus != "":
		return inst.ContainerStatus
	case inst.DesiredState == store.StateStopped:
		return colorize(ansiFaint, string(inst.DesiredState))
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
