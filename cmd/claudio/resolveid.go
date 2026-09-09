package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/rodrigomorales/claudio/internal/client"
	"github.com/rodrigomorales/claudio/internal/instancefile"
	"github.com/rodrigomorales/claudio/internal/store"
)

// instanceLookup resolves one id against the store. Taking this as a
// function rather than a client.Client is what lets resolveInstanceID's
// rules — one-vs-many, stale skipping, the wording of every error — be
// tested without a Docker-backed client or a real state.db.
type instanceLookup func(idOrName string) (store.Instance, error)

// lookupVia adapts a Client to instanceLookup.
func lookupVia(ctx context.Context, c client.Client) instanceLookup {
	return func(idOrName string) (store.Instance, error) {
		return c.GetInstance(ctx, idOrName)
	}
}

// resolveInstanceID turns a command's positional arguments into the
// instance id to act on, falling back to the nearest `.claudio` file
// when no id was given (ROD-117).
//
// The rules, in order:
//
//   - An explicit id always wins, and short-circuits before the file is
//     even read — a fully specified command must never be blocked by an
//     ambiguous or malformed file it didn't need.
//   - Exactly one live tied instance is used implicitly.
//   - More than one is refused, listing them with branch and status so
//     the user can pick. The detail comes from the store at display
//     time, which is why the file itself can stay a bare id list.
//   - An id in the file with no matching instance is stale: warned about
//     and skipped, never silently dropped. Skipping keeps the command
//     working when one of two entries was destroyed out of band; the
//     warning keeps the file's drift visible.
//
// The returned warning is for the caller to print to stderr; it is not
// an error and does not stop the command. The file is never rewritten
// here — pruning stale ids is `destroy`'s job, and a transiently
// unreadable store must not cause this to quietly edit the user's file.
func resolveInstanceID(args []string, startDir string, lookup instanceLookup) (id, warning string, err error) {
	if len(args) > 1 {
		return "", "", fmt.Errorf("expected at most one instance id, got %d (%s)", len(args), strings.Join(args, " "))
	}
	if len(args) == 1 {
		return args[0], "", nil
	}

	dir, ids, err := instancefile.Find(startDir)
	if err != nil {
		return "", "", err
	}
	if dir == "" || len(ids) == 0 {
		return "", "", fmt.Errorf("no instance id given, and no %s file found in this directory or any parent.\n"+
			"  Pass an id (see `claudio ls`), or run this from a directory where you ran `claudio create .`",
			instancefile.FileName)
	}

	var live []store.Instance
	var stale []string
	for _, candidate := range ids {
		inst, lookupErr := lookup(candidate)
		if lookupErr != nil {
			stale = append(stale, candidate)
			continue
		}
		live = append(live, inst)
	}

	if len(stale) > 0 {
		subject := fmt.Sprintf("an instance that no longer exists: %s", stale[0])
		line := "line"
		if len(stale) > 1 {
			subject = fmt.Sprintf("%d instances that no longer exist: %s", len(stale), strings.Join(stale, ", "))
			line = "lines"
		}
		warning = fmt.Sprintf("! %s lists %s — skipping.\n  Remove the %s from %s to silence this.",
			instancefile.FileName, subject, line, filepath.Join(dir, instancefile.FileName))
	}

	switch len(live) {
	case 0:
		return "", "", fmt.Errorf("every instance listed in %s/%s is gone (%s).\n"+
			"  Pass an id (see `claudio ls`), or run `claudio create .` to make a new one",
			dir, instancefile.FileName, strings.Join(stale, ", "))
	case 1:
		return live[0].ID, warning, nil
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "%d instances are tied to %s:\n", len(live), dir)
		tw := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)
		for _, inst := range live {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", inst.ID, inst.Branch, inst.DesiredState)
		}
		tw.Flush()
		fmt.Fprintf(&b, "Pass one explicitly, e.g. %s", live[0].ID)
		return "", warning, fmt.Errorf("%s", b.String())
	}
}

// resolveIDWithClient is the shape every command uses: resolve against
// the real store, print any staleness warning to stderr, and return
// ok=false with the error already reported so the caller can just
// `return 1`.
func resolveIDWithClient(ctx context.Context, c client.Client, args []string, caller string) (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", caller, err)
		return "", false
	}

	id, warning, err := resolveInstanceID(args, cwd, lookupVia(ctx, c))
	if warning != "" {
		fmt.Fprintln(os.Stderr, warning)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", caller, err)
		return "", false
	}
	return id, true
}

// recordInstance ties a newly created instance to the directory the user
// ran `claudio create .` from, and returns a notice to print when the
// directory now holds more than one — at which point bare commands stop
// being able to infer an id, and the user should hear that from create
// rather than discovering it at their next `claudio attach`.
//
// The notice is returned rather than printed so the caller can place it
// after its own "Created ..." summary; leading with it reads as though
// something went wrong before the instance was made.
//
// A failure to write is warned about rather than fatal: the instance
// itself was created successfully, and losing the convenience pointer is
// not worth failing that.
func recordInstance(sourceDir, id string) (notice string) {
	if err := instancefile.Append(sourceDir, id); err != nil {
		fmt.Fprintf(os.Stderr, "! could not record this instance in %s: %v\n",
			filepath.Join(sourceDir, instancefile.FileName), err)
		return ""
	}

	ids, _, err := instancefile.Load(sourceDir)
	if err != nil || len(ids) <= 1 {
		return ""
	}
	return fmt.Sprintf("This directory now has %d instances (%s), so `claudio attach` needs an id.",
		len(ids), strings.Join(ids, ", "))
}

// forgetInstance drops a destroyed instance's id from the pointer file,
// if the command was run from the directory that owns it. Best-effort
// and silent: destroy has already succeeded by this point, and a stale
// line left behind is handled gracefully by resolveInstanceID anyway.
func forgetInstance(id string) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	dir, _, err := instancefile.Find(cwd)
	if err != nil || dir == "" {
		return
	}
	_ = instancefile.Remove(dir, id)
}
