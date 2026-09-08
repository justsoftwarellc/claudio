package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"testing"

	"github.com/rodrigomorales/claudio/internal/engine"
)

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it. cmdLs (and every other command) writes
// directly to os.Stdout/fmt.Println rather than through an injectable
// writer, so this is the only way to assert on actual printed content
// rather than just exit codes.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return string(out)
}

func TestLsJSONOutputsValidJSONWithBothKeys(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	var code int
	out := captureStdout(t, func() {
		code = run([]string{"ls", "--json"})
	})
	if code != 0 {
		t.Fatalf("claudio ls --json exited %d, want 0", code)
	}

	var decoded struct {
		Instances []interface{} `json:"instances"`
		Untracked []interface{} `json:"untracked"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
	}
	if decoded.Instances == nil {
		t.Error(`"instances" key is null, want an empty array ([]) for the no-instances case`)
	}
	if decoded.Untracked == nil {
		t.Error(`"untracked" key is null, want an empty array ([]) for the no-untracked case`)
	}
}

// TestLsJSONReflectsRealInstance exercises the actual wire content
// against a real create → ls --json round trip, not just the
// empty-store shape above.
func TestLsJSONReflectsRealInstance(t *testing.T) {
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
	containerID := instances[0].ContainerID
	c.Close()
	t.Cleanup(func() {
		if containerID != nil {
			exec.Command("docker", "rm", "-f", *containerID).Run()
		}
	})

	out := captureStdout(t, func() {
		if code := run([]string{"ls", "--json"}); code != 0 {
			t.Errorf("claudio ls --json exited %d, want 0", code)
		}
	})

	var decoded struct {
		Instances []struct {
			ID     string `json:"id"`
			Branch string `json:"branch"`
		} `json:"instances"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
	}
	if len(decoded.Instances) != 1 {
		t.Fatalf("decoded %d instance(s), want 1: %s", len(decoded.Instances), out)
	}
	if decoded.Instances[0].ID != instances[0].ID {
		t.Errorf("decoded ID = %q, want %q", decoded.Instances[0].ID, instances[0].ID)
	}
}
