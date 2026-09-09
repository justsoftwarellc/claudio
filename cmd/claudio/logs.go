package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// cmdLogs implements `claudio logs <id> [--service X] [--follow]`
// (docs/architecture.md §6.4: "Sidecar logs reachable via claudio logs
// <id> --service db"). Like attach, this deliberately bypasses the
// Client interface and syscall.Execs into the real `docker`/`docker
// compose` CLI rather than proxying output through this process — a
// streaming --follow especially benefits from the exact same
// argument-passing this package already established for attach (no
// hop, no buffering, correct signal handling on Ctrl-C).
//
// For a compose-project instance, --service names one sidecar (or the
// agent service itself); omitted, `docker compose logs` returns every
// service interleaved. For an ordinary single-container instance,
// --service is rejected — there is only ever the one container, naming
// a service makes no sense and the flag exists only for the compose
// case (docs/architecture.md §6.4 introduces it specifically for
// sidecars).
func cmdLogs(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: claudio logs <id> [--service X] [--follow]")
		return 1
	}
	id := args[0]
	var service string
	var follow bool
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--service":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "claudio logs: --service requires a value")
				return 1
			}
			service = args[i]
		case "--follow", "-f":
			follow = true
		default:
			fmt.Fprintf(os.Stderr, "claudio logs: unknown flag %q\n", args[i])
			return 1
		}
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio logs:", describeErr(err))
		return 1
	}
	inst, err := c.GetInstance(ctx, id)
	if err != nil {
		c.Close()
		fmt.Fprintln(os.Stderr, "claudio logs:", describeErr(err))
		return 1
	}
	c.Close() // done with the store; the exec below replaces this process anyway

	if !inst.IsCompose() && service != "" {
		fmt.Fprintf(os.Stderr, "claudio logs: --service %q: %s is not a compose project — it has only one container, --service does not apply\n", service, inst.ID)
		return 1
	}

	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio logs: docker not found on PATH:", err)
		return 1
	}

	var execArgs []string
	if inst.IsCompose() {
		execArgs = []string{"docker", "compose", "-p", *inst.ComposeProject, "logs"}
		if service != "" {
			execArgs = append(execArgs, service)
		}
	} else {
		execArgs = []string{"docker", "logs", "claudio-" + inst.ID}
	}
	if follow {
		execArgs = append(execArgs, "-f")
	}

	if err := syscall.Exec(dockerPath, execArgs, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "claudio logs: exec docker:", err)
		return 1
	}
	return 0 // unreachable: syscall.Exec only returns on error
}
