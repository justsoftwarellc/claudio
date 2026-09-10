package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/rodrigomorales/claudio/internal/client"
	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/core"
	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/portdetect"
	"github.com/rodrigomorales/claudio/internal/repo"
)

// cmdCreate implements `claudio create <repo|path> [--branch B | --new-branch
// B] [--name N] [--ports c,...] [--publish-all-interfaces] [--memory M]
// [--cpus N] [--pids N] [--env-file F] [--clean-on-fail] [--yes]
// [--base-branch B] [--no-refresh]`, plus
// the greenfield `claudio create --new <name>` path (no upstream repo —
// docs/architecture.md §5.1). See §5.1/§9 and ROD-100. The credential
// comes from credentialEnv (see env.go).
func cmdCreate(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: claudio create <repo|path> [--branch B | --new-branch B] [--name N] [--ports container,...] [--publish-all-interfaces] [--memory M] [--cpus N] [--pids N] [--env-file F] [--clean-on-fail] [--yes] [--base-branch B] [--no-refresh]")
		fmt.Fprintln(os.Stderr, "   or: claudio create --new <name> [--new-branch B] [--name N] [--ports container,...] [--publish-all-interfaces] [--memory M] [--cpus N] [--pids N] [--env-file F] [--clean-on-fail]")
		return 1
	}

	var repoURL, greenfieldName, branch, newBranch, name, portsFlag, envFile, memory string
	var cleanOnFail, assumeYes, publishAllInterfaces, noRefresh bool
	var baseBranch string
	var cpus, pids int
	var cpusSet, pidsSet bool

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
		case "--publish-all-interfaces":
			publishAllInterfaces = true
		case "--memory":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "claudio create: --memory requires a value (e.g. 8g)")
				return 1
			}
			memory = args[i]
		case "--cpus":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "claudio create: --cpus requires a value")
				return 1
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				fmt.Fprintf(os.Stderr, "claudio create: --cpus: %q is not a positive integer\n", args[i])
				return 1
			}
			cpus, cpusSet = n, true
		case "--pids":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "claudio create: --pids requires a value")
				return 1
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				fmt.Fprintf(os.Stderr, "claudio create: --pids: %q is not a positive integer\n", args[i])
				return 1
			}
			pids, pidsSet = n, true
		case "--clean-on-fail":
			cleanOnFail = true
		case "--yes":
			assumeYes = true
		case "--no-refresh":
			noRefresh = true
		case "--base-branch":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "claudio create: --base-branch requires a value")
				return 1
			}
			baseBranch = args[i]
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
		fmt.Fprintln(os.Stderr, "claudio create:", describeErr(err))
		return 1
	}

	var resourceOverride *config.Resources
	if memory != "" || cpusSet || pidsSet {
		if memory != "" {
			if _, err := engine.ParseMemory(memory); err != nil {
				fmt.Fprintf(os.Stderr, "claudio create: --memory: %q is not a valid amount (e.g. 8g, 512m)\n", memory)
				return 1
			}
		}
		resourceOverride = &config.Resources{}
		if memory != "" {
			resourceOverride.Memory = &memory
		}
		if cpusSet {
			resourceOverride.CPUs = &cpus
		}
		if pidsSet {
			resourceOverride.PIDs = &pids
		}
	}

	// A local directory (`claudio create .`) becomes a file:// source
	// here, git-initializing it first if needed — ROD-115. Remote forms
	// pass through untouched. Done before the client call so a bad path
	// fails immediately, without generating an instance ID first.
	// A local source is also the directory the pointer file belongs in
	// (ROD-117): `claudio create .` ties the new instance to the folder
	// the user is standing in, so later commands can infer its id.
	var sourceDir string
	if repoURL != "" {
		resolved, err := repo.ResolveSource(ctx, repoURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, "claudio create:", describeErr(err))
			return 1
		}
		if strings.HasPrefix(resolved, "file://") {
			sourceDir = strings.TrimPrefix(resolved, "file://")
		}
		repoURL = resolved
	}

	// Decide what main-clone gets refreshed to before the worktree is
	// cut. A remote source refreshes from its default branch silently; a
	// local clone asks, because the user is standing in a working copy
	// that could be on any branch (see resolveBaseBranch).
	if !noRefresh && greenfieldName == "" && baseBranch == "" {
		resolved, ok := resolveBaseBranch(ctx, repoURL, assumeYes)
		if !ok {
			return 1
		}
		baseBranch = resolved
	}

	env, ok := credentialEnv("claudio create")
	if !ok {
		return 1
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio create:", describeErr(err))
		return 1
	}
	defer c.Close()

	var namePtr *string
	if name != "" {
		namePtr = &name
	}

	params := core.CreateParams{
		RepoURL:          repoURL,
		GreenfieldName:   greenfieldName,
		Branch:           branch,
		NewBranch:        newBranch,
		Name:             namePtr,
		ManualPorts:      manualPorts,
		Env:              env,
		EnvFile:          envFile,
		CleanOnFail:      cleanOnFail,
		ResourceOverride: resourceOverride,
		BaseBranch:       baseBranch,
		SkipRefresh:      noRefresh,

		PublishAllInterfaces: publishAllInterfaces,
	}

	result, err := c.Create(ctx, params, terminalProgress())
	var collision *repo.BranchCollisionError
	if errors.As(err, &collision) {
		result, err = retryCreateWithSuggestedBranch(ctx, c, params, collision, assumeYes)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio create:", describeErr(err))
		return 1
	}

	var tiedNotice string
	if sourceDir != "" {
		tiedNotice = recordInstance(sourceDir, result.InstanceID)
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
	if tiedNotice != "" {
		fmt.Println(tiedNotice)
	}
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
	return c.Create(ctx, params, terminalProgress())
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

// parseManualPorts parses --ports into portdetect.Manual entries.
//
// Only the bare `container` form is accepted. The `container:host` form
// is rejected rather than honored: host ports are allocated first-free-
// in-range and verified with a real bind() probe (store.AllocatePort,
// docs/architecture.md §6.2), which is the whole mechanism that lets a
// second instance of the same repo exist at all. Letting a flag pin an
// exact host port would reintroduce exactly the static-mapping collision
// that allocator was built to eliminate, and `UNIQUE(host_port)` would
// surface it as an opaque insert failure on whichever `claudio create`
// lost the race. Accepting the syntax and silently ignoring the value —
// what this did before — is worse still: it reads as a pin that works.
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
		if strings.Contains(entry, ":") {
			container := strings.SplitN(entry, ":", 2)[0]
			return nil, fmt.Errorf("invalid --ports entry %q: only the container port is supported (use %q) — Claudio picks the host port itself, from ports.range, so that two instances of the same repo can run at once", entry, container)
		}
		containerPort, err := strconv.Atoi(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid --ports entry %q: container port must be a number", entry)
		}
		out = append(out, portdetect.Manual{Container: containerPort})
	}
	return out, nil
}

// resolveBaseBranch decides which upstream branch main-clone is
// refreshed to before an instance's worktree is cut from it.
//
// The three source shapes get different treatment, because the question
// "which branch did you mean?" only has an obvious answer for two of
// them:
//
//   - A remote URL: no prompt. The user named a repo, so its default
//     branch is what they meant; returning "" lets core use main-clone's
//     own checked-out branch, which is that default.
//   - A local directory with an upstream: prompt. The user is standing
//     in a working copy that may be on any branch, and basing the
//     instance on the wrong one is a silent, expensive mistake — they
//     would not find out until the agent had already worked against it.
//   - A local directory with no upstream: no prompt and no refresh;
//     there is nothing to fetch from.
//
// Non-interactive-safe, the same requirement as the branch-collision
// prompt (docs/architecture.md §5.1): with --yes, or on a non-terminal
// stdin, this takes the upstream's default rather than blocking a
// script forever on a question nobody can answer.
func resolveBaseBranch(ctx context.Context, repoURL string, assumeYes bool) (string, bool) {
	src := repo.ClassifySource(ctx, repoURL)
	if src.Kind != repo.SourceLocalWithUpstream {
		// Remote: core falls back to main-clone's default branch.
		// Local-only: core skips the refresh entirely.
		return "", true
	}

	branches, err := repo.RemoteBranches(ctx, src.FetchURL)
	if err != nil || len(branches) == 0 {
		// The upstream is unreachable (offline, no access, a stale
		// remote). That is not worth failing a create over — the clone
		// on disk is still perfectly usable — so fall back to the
		// existing no-refresh behavior and say so, rather than leaving
		// the user to wonder whether they got fresh code.
		fmt.Fprintf(os.Stderr, "! could not reach %s to list branches — creating from the existing clone without refreshing.\n", src.FetchURL)
		return "", true
	}

	current := currentBranch(ctx, src.SourceDir)
	def := defaultAmong(branches, current)

	if assumeYes || !isInteractive() {
		fmt.Fprintf(os.Stderr, "Basing on %s from %s.\n", def, src.FetchURL)
		return def, true
	}

	fmt.Fprintf(os.Stderr, "%s has an upstream: %s\n", src.SourceDir, src.FetchURL)
	fmt.Fprintf(os.Stderr, "Which branch should this instance start from? [%s] ", def)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(os.Stderr, "\nclaudio create: canceled.")
		return "", false
	}
	answer := strings.TrimSpace(line)
	if answer == "" {
		answer = def
	}
	if !slices.Contains(branches, answer) {
		fmt.Fprintf(os.Stderr, "claudio create: %q is not a branch on %s\n", answer, src.FetchURL)
		return "", false
	}
	return answer, true
}

// defaultAmong picks the branch to offer: the one the source directory
// currently has checked out when the upstream also has it (the user is
// most likely to mean the branch they are looking at), else a
// conventional default, else whatever the upstream lists first.
func defaultAmong(branches []string, current string) string {
	if current != "" && slices.Contains(branches, current) {
		return current
	}
	for _, name := range []string{"main", "master"} {
		if slices.Contains(branches, name) {
			return name
		}
	}
	return branches[0]
}

// currentBranch reports the branch dir has checked out, or "" if that
// cannot be determined (detached HEAD, or not a repo).
func currentBranch(ctx context.Context, dir string) string {
	if dir == "" {
		return ""
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	if b := strings.TrimSpace(string(out)); b != "HEAD" {
		return b
	}
	return ""
}
