package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The tmux mechanics behind ROD-116. `claudio attach` itself ends in
// syscall.Exec, which replaces the test binary and so can't be called
// from a test — what these cover instead is the container-side
// behavior attach.go depends on: that a session survives its pane
// exiting, that a dead pane can be respawned, that respawn refuses to
// disturb a live pane, and that `new-session -A` attaches-or-creates.
// Each assertion is the reason one of attach.go's flags is there.

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

// tmuxSessionContainer starts a container holding a tmux session set up
// the way image/entrypoint.sh sets it up, including remain-on-exit.
func tmuxSessionContainer(t *testing.T, remainOnExit bool) string {
	t.Helper()
	out, err := exec.Command("docker", "run", "-d", "--rm", "--entrypoint", "tail",
		"claudio/base:dev", "-f", "/dev/null").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", id).Run() })

	if out, err := exec.Command("docker", "exec", id,
		"tmux", "new-session", "-d", "-s", "claude", "-c", "/tmp").CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
	if remainOnExit {
		if out, err := exec.Command("docker", "exec", id,
			"tmux", "set-option", "-t", "claude", "remain-on-exit", "on").CombinedOutput(); err != nil {
			t.Fatalf("set remain-on-exit: %v: %s", err, out)
		}
	}
	return id
}

// exitPaneShell makes the pane's shell exit, which is what a user's
// Ctrl-C sequence ultimately does once Claude Code has quit and only a
// bare shell is left in the pane.
func exitPaneShell(t *testing.T, id string) {
	t.Helper()
	exec.Command("docker", "exec", id, "tmux", "send-keys", "-t", "claude", "exit", "C-m").Run()
	time.Sleep(1500 * time.Millisecond)
}

func tmuxLS(id string) (string, error) {
	out, err := exec.Command("docker", "exec", id, "tmux", "ls").CombinedOutput()
	return string(out), err
}

// The bug itself: without remain-on-exit the pane's shell exiting takes
// down the session and the whole tmux server, which is why `claudio
// attach` reported "no sessions" while `claudio ls` still showed the
// container Up. Guards the entrypoint option against silent removal.
func TestPaneShellExitKillsSessionWithoutRemainOnExit(t *testing.T) {
	attachDockerAvailable(t)
	id := tmuxSessionContainer(t, false)

	exitPaneShell(t, id)

	out, err := tmuxLS(id)
	if err == nil {
		t.Fatalf("expected the tmux server to be gone after the pane shell exited, got sessions: %s", out)
	}
}

// The fix's first half (image/entrypoint.sh): with remain-on-exit the
// session outlives its pane's shell, so there is still something to
// attach to.
func TestRemainOnExitKeepsSessionAliveAfterPaneShellExits(t *testing.T) {
	attachDockerAvailable(t)
	id := tmuxSessionContainer(t, true)

	exitPaneShell(t, id)

	out, err := tmuxLS(id)
	if err != nil {
		t.Fatalf("session should have survived the pane shell exiting, tmux ls failed: %v: %s", err, out)
	}
	if !strings.Contains(out, "claude") {
		t.Errorf("tmux ls = %q, want the claude session still listed", out)
	}
}

// The fix's second half, part one (attach.go): a session kept alive by
// remain-on-exit has a dead pane, and attaching to that would show a
// frozen pane. attach respawns it first, giving the user a live shell.
func TestRespawnPaneRevivesADeadPane(t *testing.T) {
	attachDockerAvailable(t)
	id := tmuxSessionContainer(t, true)
	exitPaneShell(t, id)

	if out, err := exec.Command("docker", "exec", id,
		"tmux", "respawn-pane", "-t", "claude", "-c", "/tmp").CombinedOutput(); err != nil {
		t.Fatalf("respawn-pane on a dead pane: %v: %s", err, out)
	}

	out, err := exec.Command("docker", "exec", id,
		"tmux", "list-panes", "-t", "claude", "-F", "#{pane_dead}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-panes: %v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "0" {
		t.Errorf("pane_dead = %q after respawn, want %q (a live pane)", got, "0")
	}
}

// Why attach.go can run respawn-pane unconditionally: tmux refuses to
// respawn a pane whose process is still running, so the best-effort
// repair cannot disturb a healthy session's running Claude Code.
func TestRespawnPaneRefusesToDisturbALivePane(t *testing.T) {
	attachDockerAvailable(t)
	id := tmuxSessionContainer(t, true)

	exec.Command("docker", "exec", id, "tmux", "send-keys", "-t", "claude", "sleep 300", "C-m").Run()
	time.Sleep(700 * time.Millisecond)

	if err := exec.Command("docker", "exec", id,
		"tmux", "respawn-pane", "-t", "claude", "-c", "/tmp").Run(); err == nil {
		t.Fatal("respawn-pane on a live pane should fail, so attach cannot clobber a running session")
	}

	out, err := exec.Command("docker", "exec", id,
		"tmux", "list-panes", "-t", "claude", "-F", "#{pane_current_command}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-panes: %v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "sleep" {
		t.Errorf("pane command = %q, want %q — the running process must be untouched", got, "sleep")
	}
}

// The acceptance criterion: container up, session gone (a container
// created before remain-on-exit shipped, or a session killed some other
// way). `attach -t` is what produced the reported "no sessions" dead
// end; `new-session -A` is what attach.go uses instead.
func TestNewSessionACreatesAMissingSession(t *testing.T) {
	attachDockerAvailable(t)
	id := tmuxSessionContainer(t, false)
	exitPaneShell(t, id) // kills the session outright: no remain-on-exit

	if err := exec.Command("docker", "exec", id,
		"tmux", "attach", "-t", "claude").Run(); err == nil {
		t.Fatal("precondition: `tmux attach -t` should fail when the session is gone")
	}

	if out, err := exec.Command("docker", "exec", id,
		"tmux", "new-session", "-A", "-d", "-s", "claude", "-c", "/tmp").CombinedOutput(); err != nil {
		t.Fatalf("new-session -A should recreate the missing session: %v: %s", err, out)
	}

	out, err := tmuxLS(id)
	if err != nil || !strings.Contains(out, "claude") {
		t.Errorf("tmux ls = %q (err %v), want a recreated claude session", out, err)
	}
}

// The other half of attach-or-create: against a healthy instance it must
// attach to the existing session, never replace it — otherwise the fix
// would discard the running Claude Code on every ordinary attach.
func TestNewSessionAReusesAnExistingSession(t *testing.T) {
	attachDockerAvailable(t)
	id := tmuxSessionContainer(t, true)

	before, err := exec.Command("docker", "exec", id,
		"tmux", "display-message", "-p", "-t", "claude", "#{session_created}").CombinedOutput()
	if err != nil {
		t.Fatalf("display-message: %v: %s", err, before)
	}

	// A TTY is required here: when -A finds an existing session it
	// attaches, and tmux ignores -d in that case (without a terminal it
	// fails with "open terminal failed: not a terminal"). attach.go runs
	// this under `docker exec -it` from the user's own terminal, so a pty
	// is the real condition, not a test artifact — script(1) supplies one
	// without pulling in a pty dependency. Killing the client detaches;
	// the session must survive it.
	attach := exec.Command("script", "-q", "/dev/null",
		"docker", "exec", "-it", id,
		"tmux", "new-session", "-A", "-s", "claude", "-c", "/tmp")
	if err := attach.Start(); err != nil {
		t.Fatalf("attach: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)
	attach.Process.Kill()
	attach.Wait()

	after, err := exec.Command("docker", "exec", id,
		"tmux", "display-message", "-p", "-t", "claude", "#{session_created}").CombinedOutput()
	if err != nil {
		t.Fatalf("display-message: %v: %s", err, after)
	}
	if strings.TrimSpace(string(before)) != strings.TrimSpace(string(after)) {
		t.Errorf("session_created changed %q -> %q: the existing session was replaced, not reused",
			strings.TrimSpace(string(before)), strings.TrimSpace(string(after)))
	}
}
