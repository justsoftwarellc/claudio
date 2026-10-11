// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/rodrigomorales/claudio/internal/client"
	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/session"
	"github.com/rodrigomorales/claudio/internal/store"
)

// cmdAttach implements `claudio attach <id>`. Per ROD-100's explicit
// design decision, this deliberately bypasses the Client interface and
// core entirely for the actual attach: it resolves the container name
// via the store, then syscall.Execs into `docker exec -it claudio-<id>
// tmux ...`, replacing this process outright. That is what yields a
// genuine TTY — colors, resize propagation, correct signal handling.
// Inserting any hop between two TTYs (e.g. proxying through core/Client)
// costs fidelity for no benefit. Detach with the tmux prefix (Ctrl-b d);
// the session keeps running.
//
// Before that exec, it does go through Client once: c.Status to read
// Docker's live container state. A container that exited (crash, OOM,
// host reboot, or a plain `claudio stop`) still has a store row, so
// GetInstance alone can't tell — only a live Docker check can. `docker
// exec` into a non-running container fails outright, so without this
// check attach on a stopped instance dead-ends exactly like the
// tmux-session gap ROD-116 fixed: healthy in `claudio ls`, broken in
// practice. Starting it first makes attach just work, same rationale as
// the new-session -A below.
func cmdAttach(ctx context.Context, args []string) int {
	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio attach:", describeErr(err))
		return 1
	}
	idOrName, ok := resolveIDWithClient(ctx, c, args, "claudio attach")
	if !ok {
		c.Close()
		return 1
	}
	inst, ok := ensureRunningForAttach(ctx, c, idOrName)
	if !ok {
		c.Close()
		return 1
	}
	dockerHost := c.DockerHost()
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

	if err := syscall.Exec(dockerPath, execArgs, dockerExecEnv(dockerHost)); err != nil {
		fmt.Fprintln(os.Stderr, "claudio attach: exec docker:", err)
		return 1
	}
	return 0 // unreachable: syscall.Exec only returns on error
}

// ensureRunningForAttach resolves idOrName to its instance and, if
// Docker's live state says the container isn't running, starts it
// before returning — see cmdAttach's doc for why a live check is
// necessary (the store alone can't tell: a stopped instance's row looks
// identical to a running one there). Prints its own diagnostic and
// returns ok=false on any failure, so cmdAttach can just return 1.
func ensureRunningForAttach(ctx context.Context, c client.Client, idOrName string) (inst store.Instance, ok bool) {
	view, err := c.Status(ctx, idOrName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio attach:", describeErr(err))
		return store.Instance{}, false
	}
	if view.ContainerRunning {
		return view.Instance, true
	}
	env, ok := credentialEnv("claudio attach")
	if !ok {
		return store.Instance{}, false
	}
	fmt.Printf("%s is stopped — starting it\n", view.Instance.ID)
	if _, err := c.Start(ctx, view.Instance.ID, false, env, terminalProgress()); err != nil {
		fmt.Fprintln(os.Stderr, "claudio attach:", describeErr(err))
		return store.Instance{}, false
	}
	return view.Instance, true
}

// dockerExecEnv is os.Environ() with Docker's CLI hints turned off and
// DOCKER_HOST pinned, for the commands that syscall.Exec into `docker`
// and hand it the terminal outright (attach, logs).
//
// Every other Docker-touching command resolves the daemon through
// engine.DetectRuntime (global config's runtime.docker_host, then
// DOCKER_HOST, then the docker CLI's current context, then the SDK
// default — see internal/engine/probe.go). A bare syscall.Exec inherits
// none of that: it only sees whatever DOCKER_HOST/context happens to be
// ambient in the shell claudio itself was run from, which can name a
// different daemon than the one claudio resolved when it created the
// container (e.g. the docker CLI's context was switched since). Passing
// dockerHost — the same value every other command used — keeps this
// exec pointed at the daemon claudio actually believes the container is
// on; empty leaves the ambient resolution as a fallback.
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
// has deliberately set DOCKER_HOST or DOCKER_CLI_HINTS to something else
// in control — Go's exec takes the last value for a repeated key.
func dockerExecEnv(dockerHost string) []string {
	env := []string{"DOCKER_CLI_HINTS=false"}
	if dockerHost != "" {
		env = append(env, "DOCKER_HOST="+dockerHost)
	}
	return append(env, os.Environ()...)
}
