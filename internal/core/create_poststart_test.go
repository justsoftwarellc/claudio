// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/engine"
)

// waitForFile polls for a path the container writes, since post_start
// commands are detached: the function returns as soon as the process is
// spawned, so its effects appear a moment later rather than before
// runPostStart's own return. Polls instead of a fixed sleep so a fast
// machine isn't paying for a slow one's margin.
func waitForFile(t *testing.T, path string, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s (err: %v)", timeout, path, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRunPostStartRunsCommands(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	// Written after create for the same reason the post_create tests do
	// it: the worktree doesn't exist until AddWorktree runs inside
	// CreateInstance itself.
	claudioYML := filepath.Join(result.WorktreeDir, ".claudio.yml")
	if err := os.WriteFile(claudioYML, []byte("post_start:\n  - echo started > post-start-marker.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inst, err := s.GetInstance(t.Context(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if err := runPostStart(t.Context(), "", result.ContainerID, inst.RepoRoot, result.WorktreeDir, filepath.Join(result.WorktreeDir, config.FileName), nil); err != nil {
		t.Fatalf("runPostStart: %v", err)
	}

	data := waitForFile(t, filepath.Join(result.WorktreeDir, "post-start-marker.txt"), 10*time.Second)
	if string(data) != "started\n" {
		t.Errorf("marker content = %q, want %q", data, "started\n")
	}
}

func TestRunPostStartNoConfigIsNoop(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	inst, err := s.GetInstance(t.Context(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	// No .claudio.yml at all — most repos declare no post_start, so this
	// must be a silent no-op rather than an error.
	if err := runPostStart(t.Context(), "", result.ContainerID, inst.RepoRoot, result.WorktreeDir, filepath.Join(result.WorktreeDir, config.FileName), nil); err != nil {
		t.Fatalf("runPostStart with no config should be a no-op, got: %v", err)
	}
}

// TestRunPostStartDoesNotBlockOnLongRunningCommand is the property that
// forced the detached design: engine.RunInContainer io.Copy's the exec
// stream to EOF, so a foreground `npm start` would hang provisioning
// forever. A never-exiting command must still let runPostStart return.
func TestRunPostStartDoesNotBlockOnLongRunningCommand(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	claudioYML := filepath.Join(result.WorktreeDir, ".claudio.yml")
	if err := os.WriteFile(claudioYML, []byte("post_start:\n  - sleep 300\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inst, err := s.GetInstance(t.Context(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- runPostStart(t.Context(), "", result.ContainerID, inst.RepoRoot, result.WorktreeDir, filepath.Join(result.WorktreeDir, config.FileName), nil)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runPostStart: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runPostStart blocked on a long-running command; it must launch detached and return")
	}

	// And the command really is still running — returning promptly would
	// be worthless if it had merely killed the process.
	out, _, err := engine.RunInContainer(t.Context(), "", result.ContainerID, "/", "ps -o args | grep -c '[s]leep 300'")
	if err != nil {
		t.Fatalf("ps in container: %v", err)
	}
	if strings.TrimSpace(out) == "0" {
		t.Error("post_start command is not running after runPostStart returned")
	}
}

// TestRunPostStartCapturesOutputToLog: a detached command has no
// meaningful exit status at spawn time, so the log is the only place
// its real outcome shows up. If it isn't captured, a failing dev server
// is undiagnosable.
func TestRunPostStartCapturesOutputToLog(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	claudioYML := filepath.Join(result.WorktreeDir, ".claudio.yml")
	// stdout and stderr both, since a crashing server reports on stderr.
	if err := os.WriteFile(claudioYML, []byte("post_start:\n  - echo to-stdout; echo to-stderr >&2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inst, err := s.GetInstance(t.Context(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if err := runPostStart(t.Context(), "", result.ContainerID, inst.RepoRoot, result.WorktreeDir, filepath.Join(result.WorktreeDir, config.FileName), nil); err != nil {
		t.Fatalf("runPostStart: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	var log string
	for time.Now().Before(deadline) {
		out, _, err := engine.RunInContainer(t.Context(), "", result.ContainerID, "/", "cat "+PostStartLogPath)
		if err != nil {
			t.Fatalf("cat log: %v", err)
		}
		log = out
		if strings.Contains(log, "to-stdout") && strings.Contains(log, "to-stderr") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("log %q missing stdout/stderr output; want both to-stdout and to-stderr", log)
}

// TestRunPostStartQuotesCommands guards shellQuote: post_start nests the
// user's command inside `setsid sh -c ...`, one more round of shell
// parsing than post_create gets. Without quoting, a command containing
// quotes or a `$` would be re-split or expanded a second time and stop
// meaning what .claudio.yml says.
func TestRunPostStartQuotesCommands(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	claudioYML := filepath.Join(result.WorktreeDir, ".claudio.yml")
	// Single quotes in the command are what a naive '...' wrapper breaks on.
	if err := os.WriteFile(claudioYML, []byte("post_start:\n  - echo 'it works' > quoted-marker.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inst, err := s.GetInstance(t.Context(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if err := runPostStart(t.Context(), "", result.ContainerID, inst.RepoRoot, result.WorktreeDir, filepath.Join(result.WorktreeDir, config.FileName), nil); err != nil {
		t.Fatalf("runPostStart: %v", err)
	}

	data := waitForFile(t, filepath.Join(result.WorktreeDir, "quoted-marker.txt"), 10*time.Second)
	if strings.TrimSpace(string(data)) != "it works" {
		t.Errorf("marker content = %q, want %q", data, "it works")
	}
}

// TestStartInstanceRerunsPostStart is ROD-127's whole point, and the
// exact inverse of TestStartInstanceDoesNotRerunPostCreate: a restart
// replaces the container, so post_start MUST run again or nothing the
// repo needs running comes back.
func TestStartInstanceRerunsPostStart(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)
	workspaceRoot := t.TempDir()

	createParams := baseCreateParams(t, repoURL)
	createParams.WorkspaceRoot = workspaceRoot
	result, err := createForTest(t, s, createParams)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Appends, so a second run is detectable as growth rather than
	// needing the two runs to be told apart by content.
	claudioYML := filepath.Join(result.WorktreeDir, ".claudio.yml")
	if err := os.WriteFile(claudioYML, []byte("post_start:\n  - echo run >> post-start-log.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Establish the "already ran once" baseline create would have
	// produced had .claudio.yml existed at create time (it didn't, so
	// create's own internal call was a no-op).
	inst, err := s.GetInstance(t.Context(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if err := runPostStart(t.Context(), "", result.ContainerID, inst.RepoRoot, result.WorktreeDir, filepath.Join(result.WorktreeDir, config.FileName), nil); err != nil {
		t.Fatalf("runPostStart (baseline): %v", err)
	}
	logPath := filepath.Join(result.WorktreeDir, "post-start-log.txt")
	if got := string(waitForFile(t, logPath, 10*time.Second)); got != "run\n" {
		t.Fatalf("baseline log = %q, want one run", got)
	}

	if err := StopInstance(t.Context(), s, "", result.InstanceID); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}

	startParams := baseCreateParams(t, repoURL)
	startParams.WorkspaceRoot = workspaceRoot
	startResult, err := startInstanceForTest(t, s, startParams, result.InstanceID, false)
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", startResult.ContainerID).Run() })

	deadline := time.Now().Add(15 * time.Second)
	for {
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read post-start log: %v", err)
		}
		if string(data) == "run\nrun\n" {
			break // ran again on start, which is the assertion
		}
		if time.Now().After(deadline) {
			t.Fatalf("post_start log = %q, want two runs — start must re-run post_start", data)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// setupPostStart creates an instance and writes a .claudio.yml holding
// the given post_start body, returning what runPostStart needs. Shared
// by the liveness tests below, which differ only in the command they
// declare and what they expect reported.
func setupPostStart(t *testing.T, body string) (containerID, repoRoot, worktreeDir, configPath string) {
	t.Helper()
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	if err := os.WriteFile(filepath.Join(result.WorktreeDir, ".claudio.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	inst, err := s.GetInstance(t.Context(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	return result.ContainerID, inst.RepoRoot, result.WorktreeDir, filepath.Join(result.WorktreeDir, config.FileName)
}

// The bug (ROD-138): a post_start command that dies immediately was
// reported identically to one that is running, because only the
// launching shell's exit status was ever checked. `pnpm: not found` —
// the case that reached a user — is exactly this shape.
func TestRunPostStartReportsCommandThatDiesImmediately(t *testing.T) {
	containerID, repoRoot, worktreeDir, configPath := setupPostStart(t,
		"post_start:\n  - this-command-does-not-exist\n")

	var messages []string
	err := runPostStart(t.Context(), "", containerID, repoRoot, worktreeDir, configPath, func(e ProgressEvent) {
		messages = append(messages, e.Message)
	})
	// Still not fatal: a dead post_start must leave a usable instance.
	if err != nil {
		t.Fatalf("runPostStart should not fail provisioning for a dead command, got: %v", err)
	}

	joined := strings.Join(messages, "\n")
	if strings.Contains(joined, "launched") && !strings.Contains(joined, "exited") {
		t.Errorf("a dead command was reported as a successful launch:\n%s", joined)
	}
	if !strings.Contains(joined, "exited") {
		t.Errorf("no report that the command exited:\n%s", joined)
	}
	// The whole point of the fix: say *why*, not just that something is
	// wrong, so the user need not hand-type a docker exec to find out.
	if !strings.Contains(joined, "not found") {
		t.Errorf("report carries no log tail explaining the failure:\n%s", joined)
	}
}

// The counterpart that must not regress: a long-running command (the
// dev server post_start exists for) is still reported as launched, and
// the liveness check must not kill it or call it dead.
func TestRunPostStartReportsLongRunningCommandAsLaunched(t *testing.T) {
	// A sleep with a duration nothing else in the container uses, so the
	// pgrep below can only match the process post_start itself spawned —
	// createForTest runs the container's own command as `sleep 60`.
	const marker = "sleep 987"
	containerID, repoRoot, worktreeDir, configPath := setupPostStart(t,
		"post_start:\n  - "+marker+"\n")

	var messages []string
	if err := runPostStart(t.Context(), "", containerID, repoRoot, worktreeDir, configPath, func(e ProgressEvent) {
		messages = append(messages, e.Message)
	}); err != nil {
		t.Fatalf("runPostStart: %v", err)
	}

	joined := strings.Join(messages, "\n")
	if !strings.Contains(joined, "launched") {
		t.Errorf("a still-running command was not reported as launched:\n%s", joined)
	}
	if strings.Contains(joined, "exited") {
		t.Errorf("a still-running command was reported as dead:\n%s", joined)
	}

	// Still actually running after the check — the probe uses kill -0,
	// which must not signal the process it is asking about.
	out, _, err := engine.RunInContainer(t.Context(), "", containerID, "/", "pgrep -f 'sleep 987' >/dev/null && echo alive")
	if err != nil {
		t.Fatalf("RunInContainer: %v", err)
	}
	if !strings.Contains(out, "alive") {
		t.Errorf("liveness probe killed the command it was checking; pgrep output = %q", out)
	}
}
