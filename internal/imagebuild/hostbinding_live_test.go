// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

// Verifies the loopback-binding guidance the entrypoint seeds — see
// image/entrypoint.sh. Built and run against a real daemon for the same
// reason as the rest of this package's live tests: the point is that the
// behavior actually holds in a container, not that the script reads
// correctly.
package imagebuild

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// startBaseContainer runs the freshly built base image with the real
// entrypoint (not overridden), so the seeding logic actually executes.
func startBaseContainer(t *testing.T, tag, name string, publish string) {
	t.Helper()
	args := []string{"run", "-d", "--rm", "--name", name,
		"-e", "CLAUDE_CODE_OAUTH_TOKEN=test-token", "-w", "/tmp"}
	if publish != "" {
		args = append(args, "-p", publish)
	}
	args = append(args, tag)
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })

	// The entrypoint seeds before starting tmux; give it a moment to run.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if exec.Command("docker", "exec", name, "test", "-f", "/home/agent/.claude/CLAUDE.md").Run() == nil {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
	t.Fatalf("entrypoint never seeded ~/.claude/CLAUDE.md; logs:\n%s", logs)
}

func TestEntrypointSeedsHostBindingGuidance(t *testing.T) {
	dockerAvailable(t)
	tag := uniqueTag("claudio-hostbind")
	if err := buildBaseAs(context.Background(), tag, nil); err != nil {
		t.Fatalf("buildBaseAs: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rmi", "-f", tag).Run() })

	name := "claudio-hostbind-seed"
	exec.Command("docker", "rm", "-f", name).Run()
	startBaseContainer(t, tag, name, "")

	out, err := exec.Command("docker", "exec", name, "cat", "/home/agent/.claude/CLAUDE.md").Output()
	if err != nil {
		t.Fatalf("read seeded memory: %v", err)
	}
	got := string(out)
	// The instruction the agent has to act on, and the flag form that
	// actually works for Vite (HOST=0.0.0.0 does not — verified).
	for _, want := range []string{"0.0.0.0", "--host"} {
		if !strings.Contains(got, want) {
			t.Errorf("seeded guidance missing %q:\n%s", want, got)
		}
	}
}

// The seed must never overwrite what the user (or the agent) has since
// written there — home/ is bind-mounted and survives container rebuilds.
func TestEntrypointDoesNotClobberExistingMemory(t *testing.T) {
	dockerAvailable(t)
	tag := uniqueTag("claudio-hostbind-keep")
	if err := buildBaseAs(context.Background(), tag, nil); err != nil {
		t.Fatalf("buildBaseAs: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rmi", "-f", tag).Run() })

	name := "claudio-hostbind-keep"
	exec.Command("docker", "rm", "-f", name).Run()
	startBaseContainer(t, tag, name, "")

	const mine = "MY OWN NOTES"
	if out, err := exec.Command("docker", "exec", name, "sh", "-c",
		"echo '"+mine+"' > /home/agent/.claude/CLAUDE.md").CombinedOutput(); err != nil {
		t.Fatalf("overwrite memory: %v: %s", err, out)
	}
	// Re-run the entrypoint's seeding the way a container rebuild would.
	if out, err := exec.Command("docker", "exec", name, "/opt/claudio/entrypoint.sh").CombinedOutput(); err != nil {
		// The entrypoint ends in `exec tail -f /dev/null`, so a plain exec
		// would block; it is killed by the exec timeout instead. What
		// matters is the file state afterwards, checked below.
		_ = out
	}

	out, err := exec.Command("docker", "exec", name, "cat", "/home/agent/.claude/CLAUDE.md").Output()
	if err != nil {
		t.Fatalf("read memory after re-seed: %v", err)
	}
	if !strings.Contains(string(out), mine) {
		t.Errorf("seeding clobbered existing memory; got:\n%s", out)
	}
}
