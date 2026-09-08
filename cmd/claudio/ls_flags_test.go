package main

import (
	"context"
	"os/exec"
	"testing"

	"github.com/rodrigomorales/claudio/internal/engine"
)

func TestLsRejectsUnknownFlag(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	if code := run([]string{"ls", "--bogus-flag"}); code == 0 {
		t.Error("claudio ls --bogus-flag exited 0, want nonzero — every other command rejects unknown flags")
	}
}

func TestLsAcceptsJSONFlag(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	if code := run([]string{"ls", "--json"}); code != 0 {
		t.Errorf("claudio ls --json exited %d, want 0", code)
	}
}

func TestLsAcceptsAllFlag(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	if code := run([]string{"ls", "--all"}); code != 0 {
		t.Errorf("claudio ls --all exited %d, want 0", code)
	}
}

// TestLsHidesStoppedInstancesUnlessAll exercises the actual filtering
// against a real instance taken through create → stop, since the
// distinction only exists once an instance has a StateStopped row.
func TestLsHidesStoppedInstancesUnlessAll(t *testing.T) {
	ctx := context.Background()
	if _, err := engine.DetectRuntime(ctx, ""); err != nil {
		t.Skipf("no reachable Docker-API-compatible daemon: %v", err)
	}
	if err := exec.Command("docker", "image", "inspect", "claudio/base:dev").Run(); err != nil {
		t.Skip("claudio/base:dev image not built locally — build it with `docker build -t claudio/base:dev image/` to run this test")
	}
	if out, err := exec.Command("docker", "tag", "claudio/base:dev", "claudio/base:latest").CombinedOutput(); err != nil {
		t.Fatalf("docker tag: %v: %s", err, out)
	}

	t.Setenv("CLAUDIO_HOME", t.TempDir())
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "test-token")
	repoURL := newLocalOriginRepoForCLI(t)

	if code := run([]string{"create", repoURL}); code != 0 {
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

	if code := run([]string{"stop", id}); code != 0 {
		t.Fatalf("claudio stop exited %d, want 0", code)
	}

	// Both forms must still succeed; what differs is which rows they show,
	// asserted below against the store rather than by scraping stdout.
	if code := run([]string{"ls"}); code != 0 {
		t.Errorf("claudio ls exited %d after stop, want 0", code)
	}
	if code := run([]string{"ls", "--all"}); code != 0 {
		t.Errorf("claudio ls --all exited %d after stop, want 0", code)
	}

	c2, err := newClient(ctx)
	if err != nil {
		t.Fatalf("newClient (post-stop): %v", err)
	}
	defer c2.Close()
	after, _, err := c2.ListInstances(ctx)
	if err != nil {
		t.Fatalf("ListInstances (post-stop): %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("len(instances) after stop = %d, want 1 (stop keeps the row; only destroy deletes it)", len(after))
	}
	if after[0].DesiredState != "stopped" {
		t.Fatalf("DesiredState = %q, want \"stopped\" — the filter in cmdLs keys off this", after[0].DesiredState)
	}
}

func TestLsWithNoArgs(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	if code := run([]string{"ls"}); code != 0 {
		t.Errorf("claudio ls exited %d, want 0", code)
	}
}
