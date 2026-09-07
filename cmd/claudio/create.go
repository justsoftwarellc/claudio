package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rodrigomorales/claudio/internal/core"
	"github.com/rodrigomorales/claudio/internal/portdetect"
)

// cmdCreate implements `claudio create <repo> [--branch B | --new-branch
// B] [--name N] [--ports c:h,...] [--clean-on-fail]`. See
// docs/architecture.md §5.1/§9 and ROD-100. The credential comes from
// credentialEnv (see env.go).
func cmdCreate(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: claudio create <repo> [--branch B | --new-branch B] [--name N] [--ports container:host,...] [--clean-on-fail]")
		return 1
	}

	repoURL := args[0]
	var branch, newBranch, name, portsFlag string
	var cleanOnFail bool
	for i := 1; i < len(args); i++ {
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
		case "--clean-on-fail":
			cleanOnFail = true
		default:
			fmt.Fprintf(os.Stderr, "claudio create: unknown flag %q\n", args[i])
			return 1
		}
	}

	if branch != "" && newBranch != "" {
		fmt.Fprintln(os.Stderr, "claudio create: --branch and --new-branch are mutually exclusive")
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

	result, err := c.Create(ctx, core.CreateParams{
		RepoURL:     repoURL,
		Branch:      branch,
		NewBranch:   newBranch,
		Name:        namePtr,
		ManualPorts: manualPorts,
		Env:         env,
		CleanOnFail: cleanOnFail,
	})
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
