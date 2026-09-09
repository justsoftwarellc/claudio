package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/rodrigomorales/claudio/internal/core"
	"github.com/rodrigomorales/claudio/internal/instancefile"
	"github.com/rodrigomorales/claudio/internal/store"
)

// choiceCanceled is parseChoice's answer for "the user backed out" — a
// deliberate cancel, distinct from both a valid selection and an error.
const choiceCanceled = -1

// cmdLink implements `claudio link [<id>]` (ROD-120): ties an existing
// instance to the current directory by writing the same `.claudio` file
// `claudio create .` does, so bare `claudio attach` and friends can infer
// its id (ROD-117).
//
// This exists because create is not the only way an instance comes to
// exist in a directory the user works from: an instance created from a
// remote URL has no local source directory to record, an adopted one has
// no create-time directory at all, and every instance predating ROD-117
// was never recorded. Without link, all of those can only be tied to a
// folder by hand-writing YAML.
func cmdLink(ctx context.Context, args []string) int {
	if len(args) > 1 {
		fmt.Fprintln(os.Stderr, "Usage: claudio link [<id>]")
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio link:", err)
		return 1
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio link:", describeErr(err))
		return 1
	}
	defer c.Close()

	var inst store.Instance
	if len(args) == 1 {
		inst, err = c.GetInstance(ctx, args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "claudio link:", describeErr(err))
			return 1
		}
	} else {
		chosen, ok := pickInstance(ctx, c, cwd, "claudio link")
		if !ok {
			return 1
		}
		inst = chosen
	}

	msg, err := linkInstance(cwd, inst)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio link:", describeErr(err))
		return 1
	}
	fmt.Print(msg)
	return 0
}

// cmdUnlink implements `claudio unlink [<id>]`: drops an instance from
// this directory's `.claudio` without stopping, destroying, or otherwise
// touching the instance itself.
//
// The counterpart to link, and the supported way to clear the stale
// entry ROD-117's warning reports — which otherwise tells the user to go
// edit the file by hand.
func cmdUnlink(ctx context.Context, args []string) int {
	if len(args) > 1 {
		fmt.Fprintln(os.Stderr, "Usage: claudio unlink [<id>]")
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio unlink:", err)
		return 1
	}

	dir, ids, err := instancefile.Find(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio unlink:", describeErr(err))
		return 1
	}
	if dir == "" || len(ids) == 0 {
		fmt.Fprintf(os.Stderr, "claudio unlink: no %s file in this directory or any parent — nothing to unlink.\n",
			instancefile.FileName)
		return 1
	}

	target := ""
	switch {
	case len(args) == 1:
		target = args[0]
	case len(ids) == 1:
		target = ids[0]
	default:
		// Unlink deliberately picks from what the FILE lists, not from
		// every instance in the store: the choice here is "which of this
		// directory's links to drop", and a stale id is exactly the case
		// most worth being able to select.
		chosen, ok := pickLinkedID(dir, ids, "claudio unlink")
		if !ok {
			return 1
		}
		target = chosen
	}

	msg, err := unlinkInstance(dir, target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio unlink:", describeErr(err))
		return 1
	}
	fmt.Print(msg)
	return 0
}

// linkInstance records inst in dir's pointer file and returns the message
// to print. Idempotent, mirroring instancefile.Append: linking twice says
// so rather than reporting a second successful link.
//
// A repo that doesn't correspond to dir is reported but not refused — the
// heuristic in repoMatchesDirectory is a convenience against typo'd ids,
// not an authority on what the user meant, and a link costs one `claudio
// unlink` to undo.
func linkInstance(dir string, inst store.Instance) (string, error) {
	existing, _, err := instancefile.Load(dir)
	if err != nil {
		return "", err
	}
	for _, id := range existing {
		if id == inst.ID {
			return fmt.Sprintf("%s is already linked to this directory.\n", inst.ID), nil
		}
	}

	if err := instancefile.Append(dir, inst.ID); err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Linked %s to %s.\n", inst.ID, dir)
	if !repoMatchesDirectory(inst.RepoURL, dir) && inst.RepoURL != "" {
		fmt.Fprintf(&b, "! Note: %s was created from %s, which doesn't look like this directory.\n",
			inst.ID, inst.RepoURL)
		fmt.Fprintf(&b, "  Undo with `claudio unlink %s` if that isn't what you wanted.\n", inst.ID)
	}

	// Only worth saying when the link actually enables a bare command;
	// with several linked, ROD-117's rule requires an explicit id anyway.
	if len(existing) == 0 {
		b.WriteString("  claudio attach   # now works here without an id\n")
	} else {
		fmt.Fprintf(&b, "  This directory now has %d instances, so bare commands need an id.\n", len(existing)+1)
	}
	return b.String(), nil
}

// unlinkInstance removes id from dir's pointer file. Unlike
// instancefile.Remove, an id that was never linked is an error here: the
// user named something specific and nothing happened, which they should
// hear about rather than see reported as success.
func unlinkInstance(dir, id string) (string, error) {
	ids, _, err := instancefile.Load(dir)
	if err != nil {
		return "", err
	}

	found := false
	for _, existing := range ids {
		if existing == id {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("%s is not linked to %s (linked: %s)", id, dir, strings.Join(ids, ", "))
	}

	if err := instancefile.Remove(dir, id); err != nil {
		return "", err
	}

	if len(ids) == 1 {
		return fmt.Sprintf("Unlinked %s; removed %s.\nThe instance itself is untouched — see `claudio ls`.\n",
			id, filepath.Join(dir, instancefile.FileName)), nil
	}
	return fmt.Sprintf("Unlinked %s from %s.\nThe instance itself is untouched — see `claudio ls`.\n", id, dir), nil
}

// pickInstance prompts for one of the store's instances. Reports its own
// errors (naming caller) and returns ok=false, so callers just return 1.
func pickInstance(ctx context.Context, c interface {
	ListInstances(ctx context.Context) ([]core.InstanceView, []core.UntrackedContainer, error)
}, cwd, caller string) (store.Instance, bool) {
	instances, _, err := c.ListInstances(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, caller+":", describeErr(err))
		return store.Instance{}, false
	}
	if len(instances) == 0 {
		fmt.Fprintf(os.Stderr, "%s: no instances exist yet — create one with `claudio create .`\n", caller)
		return store.Instance{}, false
	}

	// Surface a likely match first: with several instances, the one whose
	// repo corresponds to this directory is almost always the intended
	// one, and putting it at the top makes the common answer "1".
	ordered := make([]core.InstanceView, 0, len(instances))
	for _, inst := range instances {
		if repoMatchesDirectory(inst.RepoURL, cwd) {
			ordered = append(ordered, inst)
		}
	}
	for _, inst := range instances {
		if !repoMatchesDirectory(inst.RepoURL, cwd) {
			ordered = append(ordered, inst)
		}
	}

	choices := formatInstanceChoices(ordered)
	if !isInteractive() {
		fmt.Fprintf(os.Stderr, "%s: no instance id given, and stdin is not a terminal to prompt on.\n%sPass one explicitly, e.g. `claudio link %s`.\n",
			caller, choices, ordered[0].ID)
		return store.Instance{}, false
	}

	fmt.Fprintln(os.Stderr, "Which instance should this directory use?")
	fmt.Fprint(os.Stderr, choices)
	idx, ok := promptChoice(len(ordered), caller)
	if !ok {
		return store.Instance{}, false
	}
	return ordered[idx].Instance, true
}

// pickLinkedID prompts for one of the ids already in the pointer file.
func pickLinkedID(dir string, ids []string, caller string) (string, bool) {
	var b strings.Builder
	for i, id := range ids {
		fmt.Fprintf(&b, "  %d) %s\n", i+1, id)
	}
	choices := b.String()

	if !isInteractive() {
		fmt.Fprintf(os.Stderr, "%s: %d instances are linked to %s, and stdin is not a terminal to prompt on.\n%sPass one explicitly, e.g. `claudio unlink %s`.\n",
			caller, len(ids), dir, choices, ids[0])
		return "", false
	}

	fmt.Fprintf(os.Stderr, "Which instance should be unlinked from %s?\n", dir)
	fmt.Fprint(os.Stderr, choices)
	idx, ok := promptChoice(len(ids), caller)
	if !ok {
		return "", false
	}
	return ids[idx], true
}

// promptChoice reads a selection from stdin, re-asking on bad input. A
// closed stdin ends the loop rather than spinning on EOF forever — the
// same hazard cmdCreate's prompt guards against, reachable here even on
// a terminal if the user sends EOF.
func promptChoice(count int, caller string) (int, bool) {
	reader := bufio.NewReader(os.Stdin)
	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprintf(os.Stderr, "Choose [1-%d, or q to cancel]: ", count)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			fmt.Fprintf(os.Stderr, "\n%s: canceled.\n", caller)
			return 0, false
		}
		idx, err := parseChoice(line, count)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %v\n", err)
			continue
		}
		if idx == choiceCanceled {
			fmt.Fprintf(os.Stderr, "%s: canceled.\n", caller)
			return 0, false
		}
		return idx, true
	}
	fmt.Fprintf(os.Stderr, "%s: no valid choice after 3 attempts — canceled.\n", caller)
	return 0, false
}

// parseChoice turns a prompt answer into a zero-based index, or
// choiceCanceled for "q". Out-of-range and non-numeric answers are
// errors the caller re-prompts on.
func parseChoice(input string, count int) (int, error) {
	s := strings.TrimSpace(input)
	if strings.EqualFold(s, "q") {
		return choiceCanceled, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number — enter 1-%d, or q to cancel", s, count)
	}
	if n < 1 || n > count {
		return 0, fmt.Errorf("%d is out of range — enter 1-%d, or q to cancel", n, count)
	}
	return n - 1, nil
}

// formatInstanceChoices renders the numbered picker. The id is included
// because it is what the user types for every subsequent command, and
// what the non-interactive path tells them to pass.
func formatInstanceChoices(instances []core.InstanceView) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)
	for i, inst := range instances {
		fmt.Fprintf(tw, "  %d)\t%s\t%s\t%s\n", i+1, inst.ID, inst.Branch, repoLabel(inst.RepoURL))
	}
	tw.Flush()
	return b.String()
}

// repoLabel shortens a repo URL to something that fits a picker line —
// "acme/web" for a remote, the directory name for a local source.
func repoLabel(repoURL string) string {
	switch {
	case repoURL == "":
		return "—"
	case strings.HasPrefix(repoURL, "file://"):
		return filepath.Base(strings.TrimPrefix(repoURL, "file://"))
	case strings.HasPrefix(repoURL, "local:"):
		return strings.TrimPrefix(repoURL, "local:")
	}

	trimmed := strings.TrimSuffix(repoURL, ".git")
	if i := strings.LastIndex(trimmed, ":"); i != -1 && !strings.Contains(trimmed[i+1:], "/") {
		return trimmed[i+1:] // scp-like without a path separator
	}
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return trimmed
}

// repoMatchesDirectory reports whether an instance's repo plausibly
// corresponds to dir, used to order the picker and to flag a likely
// mistaken link.
//
// Deliberately a heuristic, and deliberately advisory: a user may keep a
// checkout anywhere, so a false negative must never block a link — it
// only adds a note. The comparison is by basename for remotes (the
// overwhelmingly common case is a clone named after its repo) and by
// exact path for a local source, where the answer is knowable.
func repoMatchesDirectory(repoURL, dir string) bool {
	if repoURL == "" {
		return false
	}
	if strings.HasPrefix(repoURL, "file://") {
		source := strings.TrimPrefix(repoURL, "file://")
		if resolved, err := filepath.EvalSymlinks(source); err == nil {
			source = resolved
		}
		target := dir
		if resolved, err := filepath.EvalSymlinks(target); err == nil {
			target = resolved
		}
		return source == target
	}
	return strings.EqualFold(repoLabelBase(repoURL), filepath.Base(dir))
}

func repoLabelBase(repoURL string) string {
	label := repoLabel(repoURL)
	if i := strings.LastIndex(label, "/"); i != -1 {
		return label[i+1:]
	}
	return label
}
