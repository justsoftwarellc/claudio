package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// cmdAttach implements `claudio attach <id>`. Per ROD-100's explicit
// design decision, this deliberately bypasses the Client interface and
// core entirely: it resolves the container name via the store, then
// syscall.Execs into `docker exec -it claudio-<id> tmux attach -t
// claude`, replacing this process outright. That is what yields a
// genuine TTY — colors, resize propagation, correct signal handling.
// Inserting any hop between two TTYs (e.g. proxying through core/Client)
// costs fidelity for no benefit. Detach with the tmux prefix (Ctrl-b d);
// the session keeps running.
func cmdAttach(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: claudio attach <id>")
		return 1
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio attach:", err)
		return 1
	}
	inst, err := c.GetInstance(ctx, args[0])
	if err != nil {
		c.Close()
		fmt.Fprintln(os.Stderr, "claudio attach:", err)
		return 1
	}
	c.Close() // done with the store; the exec below replaces this process anyway

	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio attach: docker not found on PATH:", err)
		return 1
	}

	containerName := "claudio-" + inst.ID
	execArgs := []string{"docker", "exec", "-it", containerName, "tmux", "attach", "-t", "claude"}

	if err := syscall.Exec(dockerPath, execArgs, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "claudio attach: exec docker:", err)
		return 1
	}
	return 0 // unreachable: syscall.Exec only returns on error
}
