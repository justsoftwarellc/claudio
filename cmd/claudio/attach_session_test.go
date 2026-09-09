package main

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rodrigomorales/claudio/internal/session"
)

// The tmux mechanics behind ROD-116. `claudio attach` itself ends in
// syscall.Exec, which replaces the test binary and so can't be called
// from a test — what these cover instead is the container-side behavior
// attach.go and image/entrypoint.sh depend on: that the pane's process
// never exits (so the session can't die), that a session recreated by
// attach comes back running Claude Code rather than a bare shell, and
// that an ordinary attach reuses the session it finds.

func attachDockerAvailable(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping docker-backed test in -short mode")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not reachable")
	}
	if err := exec.Command("docker", "image", "inspect", "claudio/base:dev").Run(); err != nil {
		t.Skip("claudio/base:dev image not built locally — build it with `docker build -t claudio/base:dev image/` to run this test")
	}
}

func runningContainer(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("docker", "run", "-d", "--rm", "--entrypoint", "tail",
		"claudio/base:dev", "-f", "/dev/null").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", id).Run() })
	return id
}

// newSession starts the session the way image/entrypoint.sh does. cmd
// empty means a bare shell — the pre-ROD-116 shape, kept so the
// regression test can still construct it.
func newSession(t *testing.T, id, cmd string) {
	t.Helper()
	args := []string{"exec", id, "tmux", "new-session", "-d", "-s", session.SessionName, "-c", "/tmp"}
	if cmd != "" {
		args = append(args, cmd)
	}
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
}

// exitPaneShell makes the pane's foreground shell exit — what a user's
// Ctrl-C sequence ultimately does once Claude Code has quit and only a
// bare shell is left in the pane.
func exitPaneShell(t *testing.T, id string) {
	t.Helper()
	exec.Command("docker", "exec", id, "tmux", "send-keys", "-t", session.SessionName, "exit", "C-m").Run()
	time.Sleep(1500 * time.Millisecond)
}

// fakeClaudePath prefixes a pane command so it picks up the stub below
// instead of the real Claude Code binary.
const fakeClaudePath = "export PATH=/tmp/fake:$PATH; "

// fakeClaude installs a stub `claude` that exits with the given status,
// so the pane command's branch on exit status can be driven both ways
// without a credential or a real agent session. /tmp because the image
// runs as a non-root user that cannot write to /usr/local/bin.
func fakeClaude(t *testing.T, id string, exitCode int) {
	t.Helper()
	script := fmt.Sprintf("mkdir -p /tmp/fake && printf '#!/bin/sh\\nexit %d\\n' > /tmp/fake/claude && chmod +x /tmp/fake/claude", exitCode)
	if out, err := exec.Command("docker", "exec", id, "sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("install fake claude: %v: %s", err, out)
	}
}

func tmuxLS(id string) (string, error) {
	out, err := exec.Command("docker", "exec", id, "tmux", "ls").CombinedOutput()
	return string(out), err
}

func paneField(t *testing.T, id, format string) string {
	t.Helper()
	out, err := exec.Command("docker", "exec", id,
		"tmux", "list-panes", "-t", session.SessionName, "-F", format).CombinedOutput()
	if err != nil {
		t.Fatalf("list-panes %s: %v: %s", format, err, out)
	}
	return strings.TrimSpace(string(out))
}

// The original bug: with a bare shell as the pane's process, exiting it
// destroys the session and — this being the only session — the entire
// tmux server, which is why attach reported "no sessions" while
// `claudio ls` still showed the container Up.
func TestBareShellPaneDiesOnExit(t *testing.T) {
	attachDockerAvailable(t)
	id := runningContainer(t)
	newSession(t, id, "")

	exitPaneShell(t, id)

	if out, err := tmuxLS(id); err == nil {
		t.Fatalf("expected the tmux server to be gone after a bare shell pane exited, got: %s", out)
	}
}

// The behavior the pane command encodes: a clean exit (quitting Claude
// Code deliberately, which exits 0) ends the session, so the user
// returns to their host shell rather than being trapped in tmux.
func TestPaneCommandEndsSessionOnCleanExit(t *testing.T) {
	attachDockerAvailable(t)
	id := runningContainer(t)
	fakeClaude(t, id, 0)

	newSession(t, id, fakeClaudePath+session.PaneCommand)
	time.Sleep(2 * time.Second)

	if _, err := tmuxLS(id); err == nil {
		t.Error("session outlived a clean Claude Code exit; quitting must return the user to the host shell")
	}
}

// The other half: a nonzero exit — a crash, a bad credential — must
// leave the session standing with a usable shell, so there is something
// to attach to and debug rather than an instance that silently vanished.
func TestPaneCommandKeepsSessionOnFailure(t *testing.T) {
	attachDockerAvailable(t)
	id := runningContainer(t)
	fakeClaude(t, id, 1)

	newSession(t, id, fakeClaudePath+session.PaneCommand)
	time.Sleep(2 * time.Second)

	out, err := tmuxLS(id)
	if err != nil {
		t.Fatalf("session should survive a failed Claude Code start: %v: %s", err, out)
	}
	// Alive is not enough — it must be a pane the user can actually type
	// into. tmux's remain-on-exit was rejected for leaving a *dead* pane.
	if got := paneField(t, id, "#{pane_dead}"); got != "0" {
		t.Errorf("pane_dead = %q, want %q — a dead pane strands an attached user", got, "0")
	}
}

// The user-visible half: a session recreated by attach must come back
// running Claude Code, not a bare container shell. Asserted on the
// command attach passes rather than on the TUI itself, since `claude`
// needs a credential; TestPaneCommandKeepsSessionOnFailure covers what
// the same command does when it can't start.
func TestRecreatedSessionRunsTheAgent(t *testing.T) {
	attachDockerAvailable(t)
	id := runningContainer(t)

	if !strings.Contains(session.PaneCommand, "claude") {
		t.Fatalf("PaneCommand = %q, want it to launch claude — a recreated session must not drop the user at a shell", session.PaneCommand)
	}

	// A failing stub keeps the pane on the fallback shell, so the
	// assertion is about the recreated session existing and being usable,
	// not about how long a real agent happens to stay up.
	fakeClaude(t, id, 1)

	// -A creates the session because none exists, applying the command.
	out, err := exec.Command("docker", "exec", id, "tmux", "new-session", "-A", "-d",
		"-s", session.SessionName, "-c", "/tmp", fakeClaudePath+session.PaneCommand).CombinedOutput()
	if err != nil {
		t.Fatalf("new-session -A: %v: %s", err, out)
	}
	time.Sleep(2 * time.Second)

	if got := paneField(t, id, "#{pane_dead}"); got != "0" {
		t.Errorf("pane_dead = %q, want a live pane in the recreated session", got)
	}
}

// The acceptance criterion: container up, session gone. `attach -t` is
// what produced the reported "no sessions" dead end.
func TestNewSessionACreatesAMissingSession(t *testing.T) {
	attachDockerAvailable(t)
	id := runningContainer(t)
	newSession(t, id, "")
	exitPaneShell(t, id) // bare shell: kills the session outright

	if err := exec.Command("docker", "exec", id,
		"tmux", "attach", "-t", session.SessionName).Run(); err == nil {
		t.Fatal("precondition: `tmux attach -t` should fail when the session is gone")
	}

	fakeClaude(t, id, 1)
	if out, err := exec.Command("docker", "exec", id, "tmux", "new-session", "-A", "-d",
		"-s", session.SessionName, "-c", "/tmp", fakeClaudePath+session.PaneCommand).CombinedOutput(); err != nil {
		t.Fatalf("new-session -A should recreate the missing session: %v: %s", err, out)
	}

	out, err := tmuxLS(id)
	if err != nil || !strings.Contains(out, session.SessionName) {
		t.Errorf("tmux ls = %q (err %v), want a recreated session", out, err)
	}
}

// The other half of attach-or-create: against a healthy instance it must
// attach to the existing session, never replace it — otherwise every
// ordinary attach would discard the running Claude Code.
func TestNewSessionAReusesAnExistingSession(t *testing.T) {
	attachDockerAvailable(t)
	id := runningContainer(t)
	fakeClaude(t, id, 1)
	newSession(t, id, fakeClaudePath+session.PaneCommand)
	time.Sleep(1500 * time.Millisecond)

	before, err := exec.Command("docker", "exec", id, "tmux",
		"display-message", "-p", "-t", session.SessionName, "#{session_created}").CombinedOutput()
	if err != nil {
		t.Fatalf("display-message: %v: %s", err, before)
	}

	// A TTY is required: when -A finds an existing session it attaches,
	// and tmux ignores -d in that case ("open terminal failed: not a
	// terminal" without one). attach.go runs this under `docker exec -it`
	// from the user's own terminal, so a pty is the real condition, not a
	// test artifact — script(1) supplies one without a pty dependency.
	// Killing the client detaches; the session must survive it.
	attach := exec.Command("script", "-q", "/dev/null",
		"docker", "exec", "-it", id, "tmux", "new-session", "-A",
		"-s", session.SessionName, "-c", "/tmp", fakeClaudePath+session.PaneCommand)
	if err := attach.Start(); err != nil {
		t.Fatalf("attach: %v", err)
	}
	time.Sleep(2 * time.Second)
	attach.Process.Kill()
	attach.Wait()

	after, err := exec.Command("docker", "exec", id, "tmux",
		"display-message", "-p", "-t", session.SessionName, "#{session_created}").CombinedOutput()
	if err != nil {
		t.Fatalf("display-message: %v: %s", err, after)
	}
	if strings.TrimSpace(string(before)) != strings.TrimSpace(string(after)) {
		t.Errorf("session_created changed %q -> %q: the existing session was replaced, not reused",
			strings.TrimSpace(string(before)), strings.TrimSpace(string(after)))
	}
}
