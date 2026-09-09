package compose

import (
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/engine"
	"gopkg.in/yaml.v3"
)

func TestGenerateOverrideProducesValidYAML(t *testing.T) {
	// ContainerWorkdir requires worktreeDir to be under repoRoot — build
	// a minimal directory relationship the same way real callers do.
	repoRoot, worktreeDir := fixRepoRootAndWorktree(t)

	out, err := GenerateOverride("claudio_test1", []string{"db", "cache"},
		[]PortRewrite{{Service: "db", ContainerPort: 5432, HostPort: 43000}},
		AgentSpec{
			InstanceID:  "test1",
			RepoURL:     "git@github.com:acme/web.git",
			CreatedAt:   1000,
			Image:       "claudio/base:latest",
			RepoRoot:    repoRoot,
			WorktreeDir: worktreeDir,
			HomeDir:     "/home/test1",
			Resources:   engine.ResourceLimits{MemoryBytes: 6 << 30, NanoCPUs: 4_000_000_000, PIDs: 512},
			Env:         map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "tok"},
		})
	if err != nil {
		t.Fatalf("GenerateOverride: %v", err)
	}

	var doc composeFile
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("generated override is not valid YAML: %v\n%s", err, out)
	}

	db, ok := doc.Services["db"]
	if !ok {
		t.Fatal("services.db missing from generated override")
	}
	if len(db.Ports) != 1 || db.Ports[0] != "43000:5432" {
		t.Errorf("db.Ports = %v, want [\"43000:5432\"]", db.Ports)
	}
	if len(db.Networks) != 1 || db.Networks[0] != "claudio_test1" {
		t.Errorf("db.Networks = %v, want [claudio_test1]", db.Networks)
	}

	cache, ok := doc.Services["cache"]
	if !ok {
		t.Fatal("services.cache missing from generated override")
	}
	if len(cache.Ports) != 0 {
		t.Errorf("cache.Ports = %v, want none — no rewrite was given for cache", cache.Ports)
	}

	agent, ok := doc.Services[AgentServiceName]
	if !ok {
		t.Fatal("services.agent missing from generated override")
	}
	if agent.Image != "claudio/base:latest" {
		t.Errorf("agent.Image = %q, want claudio/base:latest", agent.Image)
	}
	if agent.ContainerName != "claudio-test1" {
		t.Errorf("agent.ContainerName = %q, want claudio-test1", agent.ContainerName)
	}
	if agent.Environment["CLAUDE_CODE_OAUTH_TOKEN"] != "tok" {
		t.Errorf("agent.Environment = %v, want CLAUDE_CODE_OAUTH_TOKEN=tok", agent.Environment)
	}
	if agent.MemLimit != "6442450944b" {
		t.Errorf("agent.MemLimit = %q, want 6442450944b (6 GiB)", agent.MemLimit)
	}
	if agent.CPUs != "4" {
		t.Errorf("agent.CPUs = %q, want 4", agent.CPUs)
	}
	if agent.PidsLimit != 512 {
		t.Errorf("agent.PidsLimit = %d, want 512", agent.PidsLimit)
	}
	if len(agent.CapDrop) != 1 || agent.CapDrop[0] != "ALL" {
		t.Errorf("agent.CapDrop = %v, want [ALL] — same sandbox posture as the single-container path", agent.CapDrop)
	}
	foundHomeMount := false
	for _, v := range agent.Volumes {
		if strings.HasSuffix(v, ":/home/agent") {
			foundHomeMount = true
		}
	}
	if !foundHomeMount {
		t.Errorf("agent.Volumes = %v, want a mount ending :/home/agent", agent.Volumes)
	}
}

// TestOverrideStringListMarshalsWithOverrideTag pins the fix for a real
// bug found while verifying this package against a live `docker compose
// up`: Compose appends list-valued keys like ports: across -f files by
// default rather than replacing them, so layering a rewritten host port
// over the repo's own compose file published both simultaneously
// instead of replacing it (empirically confirmed via `docker compose ...
// config` before this fix — the base file's own "5432:5432" survived
// alongside the rewrite). The !override YAML tag on a sequence node is
// what makes Compose replace instead of append.
func TestOverrideStringListMarshalsWithOverrideTag(t *testing.T) {
	out, err := yaml.Marshal(overrideStringList{"43091:5432"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := strings.TrimSpace(string(out))
	want := "!override\n- 43091:5432"
	if got != want {
		t.Errorf("Marshal(overrideStringList{...}) = %q, want %q", got, want)
	}
}

func TestOverrideStringListEmptyIsOmitted(t *testing.T) {
	type wrapper struct {
		Ports overrideStringList `yaml:"ports,omitempty"`
	}
	out, err := yaml.Marshal(wrapper{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(out), "ports") {
		t.Errorf("Marshal(wrapper{}) = %q, want ports: omitted entirely for an empty list", out)
	}
}

func TestDockerCPUsFractional(t *testing.T) {
	if got := dockerCPUs(500_000_000); got != "0.5" {
		t.Errorf("dockerCPUs(500_000_000) = %q, want 0.5", got)
	}
	if got := dockerCPUs(0); got != "" {
		t.Errorf("dockerCPUs(0) = %q, want empty (unset)", got)
	}
}

func TestDockerMemLimitUnsetWhenZero(t *testing.T) {
	if got := dockerMemLimit(0); got != "" {
		t.Errorf("dockerMemLimit(0) = %q, want empty (unset)", got)
	}
}

func fixRepoRootAndWorktree(t *testing.T) (repoRoot, worktreeDir string) {
	t.Helper()
	repoRoot = t.TempDir()
	worktreeDir = repoRoot + "/worktrees/test1"
	return repoRoot, worktreeDir
}
