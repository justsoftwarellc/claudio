// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/rodrigomorales/claudio/internal/compose"
	"github.com/rodrigomorales/claudio/internal/core"
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
//
// --post-start reads core.PostStartLogPath instead of the container's
// stdout (ROD-143). It needs its own flag because post_start output is
// deliberately redirected to a file inside the container and so never
// reaches `docker logs` at all: before this, the only way to read it
// was to hand-type `docker exec claudio-<id> cat /tmp/...`, which is
// what made a failing post_start effectively undiagnosable (ROD-138).
func cmdLogs(ctx context.Context, args []string) int {
	var rest []string
	var service string
	var follow bool
	var postStart bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--service":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "claudio logs: --service requires a value")
				return 1
			}
			service = args[i]
		case "--post-start":
			postStart = true
		case "--follow", "-f":
			follow = true
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(os.Stderr, "claudio logs: unknown flag %q\n", args[i])
				return 1
			}
			rest = append(rest, args[i])
		}
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio logs:", describeErr(err))
		return 1
	}
	id, ok := resolveIDWithClient(ctx, c, rest, "claudio logs")
	if !ok {
		c.Close()
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
	// The post_start log belongs to the agent container specifically —
	// it is written by Claudio's own provisioning, not by a sidecar — so
	// pairing it with --service would name a container that has no such
	// file. Rejected rather than silently ignored.
	if postStart && service != "" {
		fmt.Fprintln(os.Stderr, "claudio logs: --post-start and --service cannot be combined — the post_start log belongs to the agent container")
		return 1
	}

	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio logs: docker not found on PATH:", err)
		return 1
	}

	var execArgs []string
	switch {
	case postStart:
		// `tail -n +1` rather than cat so --follow is just -f on the same
		// command. Note the flag goes *before* the path here, unlike the
		// docker-logs branches below where it is appended: tail takes its
		// operand last. A missing file means post_start never ran (no
		// hook declared, or the container predates one) — tail's own
		// message says so, which is more accurate than anything this
		// command could assert without inspecting the config.
		//
		// `docker compose exec` for a compose instance rather than a
		// hand-built "<project>-agent-1" container name: that name is
		// Compose's own convention, not a fact this command knows, and
		// resolving the service by name is what compose is for.
		tail := []string{"tail", "-n", "+1"}
		if follow {
			tail = append(tail, "-f")
		}
		tail = append(tail, core.PostStartLogPath)
		if inst.IsCompose() {
			execArgs = append([]string{"docker", "compose", "-p", *inst.ComposeProject, "exec", "-T", compose.AgentServiceName}, tail...)
		} else {
			execArgs = append([]string{"docker", "exec", "claudio-" + inst.ID}, tail...)
		}
	case inst.IsCompose():
		execArgs = []string{"docker", "compose", "-p", *inst.ComposeProject, "logs"}
		if service != "" {
			execArgs = append(execArgs, service)
		}
		if follow {
			execArgs = append(execArgs, "-f")
		}
	default:
		execArgs = []string{"docker", "logs", "claudio-" + inst.ID}
		if follow {
			execArgs = append(execArgs, "-f")
		}
	}

	if err := syscall.Exec(dockerPath, execArgs, dockerExecEnv()); err != nil {
		fmt.Fprintln(os.Stderr, "claudio logs: exec docker:", err)
		return 1
	}
	return 0 // unreachable: syscall.Exec only returns on error
}
