package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/rodrigomorales/claudio/internal/client"
	"github.com/rodrigomorales/claudio/internal/core"
	"github.com/rodrigomorales/claudio/internal/portdetect"
	"github.com/rodrigomorales/claudio/internal/repo"
)

// cmdCreate implements `claudio create <repo> [--branch B | --new-branch
// B] [--name N] [--ports c:h,...] [--env-file F] [--clean-on-fail]
// [--yes]`, plus the greenfield `claudio create --new <name>` path (no
// upstream repo — docs/architecture.md §5.1). See §5.1/§9 and ROD-100.
// The credential comes from credentialEnv (see env.go).
func cmdCreate(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: claudio create <repo> [--branch B | --new-branch B] [--name N] [--ports container:host,...] [--env-file F] [--clean-on-fail] [--yes]")
		fmt.Fprintln(os.Stderr, "   or: claudio create --new <name> [--new-branch B] [--name N] [--ports container:host,...] [--env-file F] [--clean-on-fail]")
		return 1
	}

	var repoURL, greenfieldName, branch, newBranch, name, portsFlag, envFile string
	var cleanOnFail, assumeYes bool

	firstArg := args[0]
	startFlags := 1
	if firstArg == "--new" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "claudio create: --new requires a name")
			return 1
		}
		greenfieldName = args[1]
		startFlags = 2
	} else {
		repoURL = firstArg
	}

	for i := startFlags; i < len(args); i++ {
		switch args[i] {
		case "--branch":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "claudio create: --branch requires a value")
				return 1
			}
			branch = args[i]
		case "--new-branch":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "claudio create: --new-branch requires a value")
				return 1
			}
			newBranch = args[i]
		case "--name":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "claudio create: --name requires a value")
				return 1
			}
			name = args[i]
		case "--ports":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "claudio create: --ports requires a value")
				return 1
			}
			portsFlag = args[i]
		case "--env-file":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "claudio create: --env-file requires a value")
				return 1
			}
			envFile = args[i]
		case "--clean-on-fail":
			cleanOnFail = true
		case "--yes":
			assumeYes = true
		default:
			fmt.Fprintf(os.Stderr, "claudio create: unknown flag %q\n", args[i])
			return 1
		}
	}

	if branch != "" && newBranch != "" {
		fmt.Fprintln(os.Stderr, "claudio create: --branch and --new-branch are mutually exclusive")
		return 1
	}
	if greenfieldName != "" && branch != "" {
		fmt.Fprintln(os.Stderr, "claudio create --new: --branch doesn't apply — a greenfield root has no existing branch to check out; use --new-branch to name the one this creates")
		return 1
	}

	manualPorts, err := parseManualPorts(portsFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio create:", err)
		return 1
	}

	env, ok := credentialEnv("claudio create")
	if !ok {
		return 1
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio create:", err)
		return 1
	}
	defer c.Close()

	var namePtr *string
	if name != "" {
		namePtr = &name
	}

	params := core.CreateParams{
		RepoURL:        repoURL,
		GreenfieldName: greenfieldName,
		Branch:         branch,
		NewBranch:      newBranch,
		Name:           namePtr,
		ManualPorts:    manualPorts,
		Env:            env,
		EnvFile:        envFile,
		CleanOnFail:    cleanOnFail,
	}

	result, err := c.Create(ctx, params)
	var collision *repo.BranchCollisionError
	if errors.As(err, &collision) {
		result, err = retryCreateWithSuggestedBranch(ctx, c, params, collision, assumeYes)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio create:", err)
		return 1
	}

	fmt.Printf("Created %s (branch %s)\n", result.InstanceID, result.Branch)
	fmt.Printf("Workspace: %s\n", result.WorktreeDir)
	if len(result.Ports) > 0 {
		parts := make([]string, 0, len(result.Ports))
		for _, p := range result.Ports {
			parts = append(parts, fmt.Sprintf("%s:%d->%d", p.ServiceName, p.ContainerPort, p.HostPort))
		}
		fmt.Printf("Ports: %s\n", strings.Join(parts, ", "))
	}
	fmt.Printf("Attach with: claudio attach %s\n", result.InstanceID)
	return 0
}

// retryCreateWithSuggestedBranch implements ROD-97's collision UX: name
// the instance already holding the branch, suggest a numeric-suffixed
// alternative, and either take it automatically (--yes) or ask — safely
// for a non-interactive invocation, which errors with the suggestion in
// the message rather than blocking on a stdin read that will never
// resolve (docs/architecture.md §5.1: "make the prompt non-interactive-
// safe").
func retryCreateWithSuggestedBranch(ctx context.Context, c client.Client, params core.CreateParams, collision *repo.BranchCollisionError, assumeYes bool) (core.CreateResult, error) {
	suggested := suggestBranchName(collision.Branch)

	fmt.Fprintf(os.Stderr, "! %s is already checked out at %s\n\n", collision.Branch, collision.WorktreeDir)

	if !assumeYes {
		if !isInteractive() {
			return core.CreateResult{}, fmt.Errorf("branch %q is taken (see above); rerun with --new-branch %s, or --yes to accept that suggestion non-interactively", collision.Branch, suggested)
		}
		fmt.Fprintf(os.Stderr, "  Create %s instead? [Y/n] ", suggested)
		var response string
		fmt.Scanln(&response)
		response = strings.ToLower(strings.TrimSpace(response))
		if response != "" && response != "y" && response != "yes" {
			return core.CreateResult{}, fmt.Errorf("branch %q is taken; rerun with --branch or --new-branch to choose one explicitly", collision.Branch)
		}
	}

	fmt.Fprintf(os.Stderr, "Creating %s instead.\n", suggested)
	params.NewBranch = suggested
	params.Branch = ""
	return c.Create(ctx, params)
}

// suggestBranchName mirrors repo.SuggestBranchName's numeric-suffix
// scheme (feat/auth -> feat/auth-2 -> feat/auth-3) but is only ever
// asked to suggest past the single collision Create's error already
// reported — it does not need repo.Root or BranchExists to check
// further collisions itself; Create will report a fresh
// BranchCollisionError if the suggestion itself is somehow also taken
// (e.g. a race with a concurrent `claudio create`), and this function is
// not called again in that case (see cmdCreate: retry happens exactly
// once).
func suggestBranchName(branch string) string {
	return branch + "-2"
}

// isInteractive reports whether stdin looks like a real terminal a user
// could actually answer a prompt on, rather than a pipe, redirect, or
// closed fd — docs/architecture.md §5.1's "non-interactive-safe" prompt.
func isInteractive() bool {
	// os.ModeCharDevice alone cannot distinguish a real terminal from
	// /dev/null (both are character devices) — found empirically while
	// testing this: redirecting stdin from /dev/null was still detected
	// as "interactive," so a redirected-but-not-obviously-piped
	// invocation (a plausible shape for cron or some CI runners) could
	// silently proceed past the [Y/n] prompt instead of failing fast,
	// defeating the whole point of "non-interactive-safe." term.IsTerminal
	// is the actual, portable check.
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// parseManualPorts parses --ports container:host,container:host into
// portdetect.Manual entries. The host side is informational only right
// now — store.AllocatePort is the actual authority on which host port an
// instance gets (see portdetect.Manual's doc) — so a supplied host value
// is validated but not threaded through; a future iteration may let
// --ports pin an exact host port once AllocatePort supports a preferred
// value.
func parseManualPorts(flag string) ([]portdetect.Manual, error) {
	if flag == "" {
		return nil, nil
	}
	var out []portdetect.Manual
	for _, entry := range strings.Split(flag, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ":", 2)
		containerPort, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid --ports entry %q: container port must be a number", entry)
		}
		if len(parts) == 2 {
			if _, err := strconv.Atoi(parts[1]); err != nil {
				return nil, fmt.Errorf("invalid --ports entry %q: host port must be a number", entry)
			}
		}
		out = append(out, portdetect.Manual{Container: containerPort})
	}
	return out, nil
}
