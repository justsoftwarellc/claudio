package compose

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/engine"
)

func dockerComposeAvailable(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping docker-backed test in -short mode")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
}

// TestUpDownAgainstRealComposeFile is this package's core empirical
// verification: a generated override, layered over a real repo compose
// file via `docker compose -f base -f override up -d`, must actually
// start both the sidecar (with its port rewritten) and the agent
// container on a shared network, and `docker compose down` must remove
// both cleanly. This is the exact mechanism docs/architecture.md §6.4
// describes, exercised against the real CLI rather than only unit-tested
// YAML shape.
func TestUpDownAgainstRealComposeFile(t *testing.T) {
	dockerComposeAvailable(t)

	dir := t.TempDir()
	worktreeDir := filepath.Join(dir, "worktrees", "verify1")
	homeDir := filepath.Join(dir, "home")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	baseFile := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(baseFile, []byte(`
services:
  db:
    image: alpine
    command: sleep 300
    ports:
      - "5432:5432"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	project := ProjectName("verify1")
	network := project + "_net"
	override, err := GenerateOverride(network, []string{"db"},
		[]PortRewrite{{Service: "db", ContainerPort: 5432, HostPort: 43091}},
		AgentSpec{
			InstanceID: "verify1",
			RepoURL:    "file:///fake",
			CreatedAt:  1,
			Image:      "alpine",
			// alpine has no long-running default command; production's
			// real claudio/base image keeps itself alive on its own
			// (image/entrypoint.sh) — this Cmd override exists only so
			// this test's fixture image stays up long enough to inspect.
			Cmd:         []string{"sleep", "300"},
			RepoRoot:    dir,
			WorktreeDir: worktreeDir,
			HomeDir:     homeDir,
			Resources:   engine.ResourceLimits{MemoryBytes: 256 << 20},
		})
	if err != nil {
		t.Fatalf("GenerateOverride: %v", err)
	}
	overridePath := filepath.Join(dir, "override.yaml")
	if err := WriteOverride(overridePath, override); err != nil {
		t.Fatal(err)
	}

	files := Files{Base: baseFile, Override: overridePath}
	t.Cleanup(func() {
		Down(context.Background(), "", project, files, true)
	})

	if out, err := Up(context.Background(), "", project, files); err != nil {
		t.Fatalf("Up: %v: %s", err, out)
	}

	states, err := Ps(context.Background(), "", project)
	if err != nil {
		t.Fatalf("Ps: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("Ps returned %d services, want 2 (db + agent): %+v", len(states), states)
	}
	byService := make(map[string]ServiceState, len(states))
	for _, s := range states {
		byService[s.Service] = s
	}
	if db, ok := byService["db"]; !ok || db.State != "running" {
		t.Errorf("db service state = %+v, want running", byService["db"])
	}
	if agent, ok := byService[AgentServiceName]; !ok || agent.State != "running" {
		t.Errorf("agent service state = %+v, want running", byService[AgentServiceName])
	}

	// The rewritten host port must actually be bound, confirmed the same
	// way `claudio ports` or a human would check: docker compose port.
	dbPortOut, err := exec.Command("docker", "compose", "-p", project, "-f", baseFile, "-f", overridePath, "port", "db", "5432").CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose port db 5432: %v: %s", err, dbPortOut)
	}
	if !strings.Contains(string(dbPortOut), "43091") {
		t.Errorf("docker compose port db 5432 = %q, want it to mention the rewritten host port 43091", dbPortOut)
	}

	// The agent container must exist under the exact name single-container
	// attach/status resolve by (docs/architecture.md §9.1) — the compose
	// path must not diverge on that naming convention.
	if err := exec.Command("docker", "inspect", "claudio-verify1").Run(); err != nil {
		t.Errorf("docker inspect claudio-verify1 failed: %v — agent container_name must match the single-container naming convention", err)
	}

	if out, err := Down(context.Background(), "", project, files, true); err != nil {
		t.Fatalf("Down: %v: %s", err, out)
	}
}
