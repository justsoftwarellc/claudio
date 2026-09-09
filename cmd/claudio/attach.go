package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/session"
)

// cmdAttach implements `claudio attach <id>`. Per ROD-100's explicit
// design decision, this deliberately bypasses the Client interface and
// core entirely: it resolves the container name via the store, then
// syscall.Execs into `docker exec -it claudio-<id> tmux ...`, replacing
// this process outright. That is what yields a genuine TTY — colors,
// resize propagation, correct signal handling. Inserting any hop between
// two TTYs (e.g. proxying through core/Client) costs fidelity for no
// benefit. Detach with the tmux prefix (Ctrl-b d); the session keeps
// running.
func cmdAttach(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: claudio attach <id>")
		return 1
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio attach:", describeErr(err))
		return 1
	}
	inst, err := c.GetInstance(ctx, args[0])
	if err != nil {
		c.Close()
		fmt.Fprintln(os.Stderr, "claudio attach:", describeErr(err))
		return 1
	}
	c.Close() // done with the store; the exec below replaces this process anyway

	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio attach: docker not found on PATH:", err)
		return 1
	}

	containerName := "claudio-" + inst.ID

	// The session's cwd inside the container, computed exactly the way
	// engine.CreateAndStart computed the container's own WorkingDir —
	// /repo/worktrees/<id>, not the host path in inst.WorktreeDir. Only
	// the recreate path below needs it; if it can't be derived (an adopted
	// container whose worktree isn't under the repo root), fall back to
	// tmux's default cwd rather than failing the attach, since attaching
	// to a healthy session doesn't need it at all.
	workdir, err := engine.ContainerWorkdir(inst.RepoRoot, inst.WorktreeDir)
	if err != nil {
		workdir = ""
	}

	// `new-session -A` rather than `attach -t`: attach if the session
	// exists, create it otherwise (ROD-116). A container whose tmux
	// session died outright — one created before the ROD-116 fix, or
	// killed by any other means — still shows as Up in `claudio ls`,
	// because tini and `tail -f` hold the container open independently of
	// tmux. `attach -t` in that state failed with tmux's bare "no
	// sessions" plus Docker's generic "try docker debug" hint: a dead end
	// mid-workflow. Attach-or-create makes the command just work,
	// matching the healthy instance the user sees in `claudio ls`.
	execArgs := []string{"docker", "exec", "-it", containerName,
		"tmux", "new-session", "-A", "-s", session.SessionName}
	if workdir != "" {
		execArgs = append(execArgs, "-c", workdir)
	}
	// The command only applies when -A actually creates the session;
	// tmux ignores it when attaching to an existing one. session.PaneCommand
	// is what the entrypoint starts the pane with, so a recreated session
	// comes back running Claude Code rather than a bare container shell.
	execArgs = append(execArgs, session.PaneCommand)

	if err := syscall.Exec(dockerPath, execArgs, dockerExecEnv()); err != nil {
		fmt.Fprintln(os.Stderr, "claudio attach: exec docker:", err)
		return 1
	}
	return 0 // unreachable: syscall.Exec only returns on error
}

// dockerExecEnv is os.Environ() with Docker's CLI hints turned off, for
// the commands that syscall.Exec into `docker` and hand it the terminal
// outright (attach, logs).
//
// On exit from an interactive `docker exec -it`, the Docker CLI prints a
// "What's next: Try Docker Debug ..." promo. Because those commands
// replace this process, that text lands in the user's terminal as if
// Claudio had printed it — so quitting a Claude Code session ended with
// an unprompted suggestion to run `docker debug claudio-<id>`, which is
// not a Claudio workflow and reads as an error where none occurred. The
// container name in it makes it look especially like Claudio's own
// output.
//
// DOCKER_CLI_HINTS=false is Docker's documented opt-out (verified: the
// block disappears). Prepending rather than appending leaves a user who
// has deliberately set it to something else in control — Go's exec takes
// the last value for a repeated key.
func dockerExecEnv() []string {
	return append([]string{"DOCKER_CLI_HINTS=false"}, os.Environ()...)
}
