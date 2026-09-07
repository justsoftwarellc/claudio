package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rodrigomorales/claudio/internal/engine"
)

// newLocalOriginRepoForCLI mirrors internal/repo and internal/core's own
// test helpers of the same shape — a bare repo on disk, so this test
// never touches the network.
func newLocalOriginRepoForCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.git")
	seed := filepath.Join(dir, "seed")

	runGitCLI(t, "", "init", "--bare", origin)
	runGitCLI(t, "", "init", seed)
	runGitCLI(t, seed, "config", "user.email", "test@example.com")
	runGitCLI(t, seed, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCLI(t, seed, "add", "README.md")
	runGitCLI(t, seed, "commit", "-m", "initial")
	runGitCLI(t, seed, "branch", "-M", "main")
	runGitCLI(t, seed, "remote", "add", "origin", origin)
	runGitCLI(t, seed, "push", "origin", "main")
	return "file://" + origin
}

func runGitCLI(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// TestCreateAttachDestroyEndToEnd exercises ROD-100's full command
// surface through the actual CLI dispatcher (run(), not the Client
// interface directly), against a real Docker daemon: `create`
// provisions a container, `cd`/`status`/`ports` inspect it, `ports
// --add`/`--remove` amend its port reservations, `stop`/`start`/
// `restart` cycle its container, and `destroy` tears it down. `attach`
// itself is not exercised here — it syscall.Execs and replaces the test
// process by design (see cmdAttach's doc) — but resolving the instance
// for it (GetInstance, container naming) is covered indirectly by `cd`
// using the same lookup path.
func TestCreateAttachDestroyEndToEnd(t *testing.T) {
	ctx := context.Background()
	if _, err := engine.DetectRuntime(ctx, ""); err != nil {
		t.Skipf("no reachable Docker-API-compatible daemon: %v", err)
	}

	t.Setenv("CLAUDIO_HOME", t.TempDir())
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "test-token")
	repoURL := newLocalOriginRepoForCLI(t)

	// cmdCreate always uses the image's own entrypoint (no Cmd override
	// exposed on the CLI, by design — see create.go, and no --image flag
	// either — ROD-100's spec doesn't call for one), so this test needs a
	// real, already-built image capable of staying up on its own.
	// client.Local.Create defaults to "claudio/base:latest"; the locally
	// built tag from image/Dockerfile (ROD-96) is ":dev", so this test
	// retags it rather than adding a test-only image override to
	// production flag parsing. Skips cleanly if the image was never
	// built locally.
	if err := exec.Command("docker", "image", "inspect", "claudio/base:dev").Run(); err != nil {
		t.Skip("claudio/base:dev image not built locally — build it with `docker build -t claudio/base:dev image/` to run this test")
	}
	if out, err := exec.Command("docker", "tag", "claudio/base:dev", "claudio/base:latest").CombinedOutput(); err != nil {
		t.Fatalf("docker tag: %v: %s", err, out)
	}

	code := run([]string{"create", repoURL})
	if code != 0 {
		t.Fatalf("claudio create exited %d, want 0", code)
	}

	c, err := newClient(ctx)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	instances, _, err := c.ListInstances(ctx)
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("len(instances) = %d, want 1", len(instances))
	}
	id := instances[0].ID
	containerID := instances[0].ContainerID
	c.Close()
	t.Cleanup(func() {
		if containerID != nil {
			exec.Command("docker", "rm", "-f", *containerID).Run()
		}
	})

	if code := run([]string{"cd", id}); code != 0 {
		t.Errorf("claudio cd %s exited %d, want 0", id, code)
	}

	if code := run([]string{"status", id}); code != 0 {
		t.Errorf("claudio status %s exited %d, want 0", id, code)
	}

	if code := run([]string{"ports", id}); code != 0 {
		t.Errorf("claudio ports %s exited %d, want 0", id, code)
	}
	if code := run([]string{"ports", id, "--add", "9229"}); code != 0 {
		t.Errorf("claudio ports %s --add 9229 exited %d, want 0", id, code)
	}
	if code := run([]string{"ports", id, "--remove", "9229"}); code != 0 {
		t.Errorf("claudio ports %s --remove 9229 exited %d, want 0", id, code)
	}

	if code := run([]string{"stop", id}); code != 0 {
		t.Errorf("claudio stop %s exited %d, want 0", id, code)
	}
	if code := run([]string{"start", id}); code != 0 {
		t.Errorf("claudio start %s exited %d, want 0", id, code)
	}
	if code := run([]string{"restart", id}); code != 0 {
		t.Errorf("claudio restart %s exited %d, want 0", id, code)
	}

	// Restart replaced the container this test's cleanup already knew
	// about — fetch the current one so cleanup removes the right ID.
	c3, err := newClient(ctx)
	if err != nil {
		t.Fatalf("newClient (post-restart): %v", err)
	}
	instancesAfterRestart, _, err := c3.ListInstances(ctx)
	c3.Close()
	if err != nil {
		t.Fatalf("ListInstances (post-restart): %v", err)
	}
	if len(instancesAfterRestart) == 1 && instancesAfterRestart[0].ContainerID != nil {
		containerID = instancesAfterRestart[0].ContainerID
	}

	if code := run([]string{"destroy", id}); code != 0 {
		t.Errorf("claudio destroy %s exited %d, want 0", id, code)
	}

	c2, err := newClient(ctx)
	if err != nil {
		t.Fatalf("newClient (post-destroy): %v", err)
	}
	defer c2.Close()
	instancesAfter, _, err := c2.ListInstances(ctx)
	if err != nil {
		t.Fatalf("ListInstances (post-destroy): %v", err)
	}
	if len(instancesAfter) != 0 {
		t.Errorf("len(instances) after destroy = %d, want 0", len(instancesAfter))
	}
}
