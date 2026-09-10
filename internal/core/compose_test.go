// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/compose"
	"github.com/rodrigomorales/claudio/internal/store"
)

func dockerComposeAvailable(t *testing.T) {
	t.Helper()
	dockerAvailable(t)
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
}

// newLocalOriginRepoWithComposeFile mirrors create_image_test.go's
// newLocalOriginRepoWithClaudioYML for a repo's own compose file instead
// of .claudio.yml — needed before CreateInstance runs since
// provisionContainer's needsCompose detection reads it from the
// worktree, same reasoning as that helper's own doc.
func newLocalOriginRepoWithComposeFile(t *testing.T, composeYAML string) string {
	t.Helper()
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.git")
	seed := filepath.Join(dir, "seed")

	runGit(t, "", "init", "--bare", origin)
	runGit(t, "", "init", seed)
	runGit(t, seed, "config", "user.email", "test@example.com")
	runGit(t, seed, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(seed, "compose.yaml"), []byte(composeYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "compose.yaml")
	runGit(t, seed, "commit", "-m", "initial")
	runGit(t, seed, "branch", "-M", "main")
	runGit(t, seed, "remote", "add", "origin", origin)
	runGit(t, seed, "push", "origin", "main")
	return "file://" + origin
}

// TestCreateInstanceWithComposeFileStartsSidecarAndAgent is this
// package's empirical verification of the compose path end to end: a
// real CreateInstance against a repo with a compose.yaml must allocate a
// host port for the sidecar's declared port, start both the sidecar and
// the agent container on a shared network, and record ComposeProject on
// the store row so the rest of the lifecycle (stop/start/destroy) knows
// to drive `docker compose` instead of a single container.
func TestCreateInstanceWithComposeFileStartsSidecarAndAgent(t *testing.T) {
	dockerComposeAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepoWithComposeFile(t, `
services:
  db:
    image: alpine
    command: ["sleep", "60"]
    ports:
      - "5432:5432"
`)

	params := baseCreateParams(t, repoURL)
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	project := compose.ProjectName(result.InstanceID)
	inst, err := s.GetInstance(context.Background(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	t.Cleanup(func() {
		files := compose.Files{Base: filepath.Join(inst.WorktreeDir, "compose.yaml"), Override: inst.WorktreeDir + ".compose-override.yml"}
		compose.Down(context.Background(), "", project, files, true)
	})

	if len(result.Ports) != 1 || result.Ports[0].ServiceName != "db" {
		t.Fatalf("Ports = %+v, want one mapping for service db", result.Ports)
	}
	hostPort := result.Ports[0].HostPort

	if !inst.IsCompose() {
		t.Fatal("IsCompose() = false, want true for a repo with its own compose.yaml")
	}
	if *inst.ComposeProject != project {
		t.Errorf("ComposeProject = %q, want %q", *inst.ComposeProject, project)
	}

	states, err := compose.Ps(context.Background(), "", project)
	if err != nil {
		t.Fatalf("Ps: %v", err)
	}
	byService := make(map[string]compose.ServiceState, len(states))
	for _, st := range states {
		byService[st.Service] = st
	}
	if db, ok := byService["db"]; !ok || db.State != "running" {
		t.Errorf("db state = %+v, want running", byService["db"])
	}
	if agent, ok := byService[compose.AgentServiceName]; !ok || agent.State != "running" {
		t.Errorf("agent state = %+v, want running", byService[compose.AgentServiceName])
	}

	// The rewritten host port, not the compose file's own hardcoded
	// 5432, must actually be published — the whole point of allocating
	// through the store rather than trusting the repo's file verbatim.
	portOut, err := exec.Command("docker", "compose", "-p", project,
		"-f", filepath.Join(inst.WorktreeDir, "compose.yaml"),
		"-f", inst.WorktreeDir+".compose-override.yml",
		"port", "db", "5432").CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose port db 5432: %v: %s", err, portOut)
	}
	if !strings.Contains(string(portOut), strconv.Itoa(hostPort)) {
		t.Errorf("docker compose port db 5432 = %q, want it to mention the allocated host port %d", portOut, hostPort)
	}
}

// TestComposeInstanceStopStartDestroyLifecycle exercises
// StopInstance/StartInstance/DestroyInstance's new compose branches
// (internal/core/stop.go, start.go via provisionContainer, destroy.go)
// against a real compose project — stop must run `docker compose down`
// (not engine.RemoveContainer, which only knows about a single
// container_id and would leave the sidecar running), start must
// re-provision the whole project again, and destroy must remove
// everything including sidecar volumes.
func TestComposeInstanceStopStartDestroyLifecycle(t *testing.T) {
	dockerComposeAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepoWithComposeFile(t, `
services:
  db:
    image: alpine
    command: ["sleep", "60"]
`)

	params := baseCreateParams(t, repoURL)
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	project := compose.ProjectName(result.InstanceID)
	t.Cleanup(func() {
		inst, err := s.GetInstance(context.Background(), result.InstanceID)
		if err != nil {
			return // already destroyed by the test itself
		}
		compose.Down(context.Background(), "", project, composeFilesForTest(inst), true)
	})

	if err := StopInstance(context.Background(), s, "", result.InstanceID); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}
	if states, _ := compose.Ps(context.Background(), "", project); len(states) != 0 {
		// compose ps --all still lists removed-but-not-pruned entries on
		// some versions; require every entry to report a terminal,
		// non-running state rather than requiring the list to be empty
		// outright.
		for _, st := range states {
			if st.State == "running" {
				t.Errorf("after StopInstance, service %s is still running", st.Service)
			}
		}
	}

	// StartInstance re-provisions the whole compose project, same as a
	// fresh create — reusing the same underlying test setup
	// createInstanceWithCmd uses (createForTest's "sleep 60" cmd) isn't
	// available here since StartInstance always goes through
	// StartInstance itself with no cmd override; the compose file
	// already gives db its own long-running command above, and the
	// agent's Cmd is threaded through by provisionCompose from
	// startInstanceWithCmd's own cmd parameter, exercised via
	// startForTest below.
	if _, err := startForTest(t, s, params, result.InstanceID); err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	states, err := compose.Ps(context.Background(), "", project)
	if err != nil {
		t.Fatalf("Ps after restart: %v", err)
	}
	foundRunningDB := false
	for _, st := range states {
		if st.Service == "db" && st.State == "running" {
			foundRunningDB = true
		}
	}
	if !foundRunningDB {
		t.Errorf("Ps after restart = %+v, want db running again", states)
	}

	if err := DestroyInstance(context.Background(), s, DestroyParams{IDOrName: result.InstanceID}); err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}
	if _, err := s.GetInstance(context.Background(), result.InstanceID); err == nil {
		t.Error("GetInstance succeeded after DestroyInstance, want the row gone")
	}
	finalStates, err := compose.Ps(context.Background(), "", project)
	if err != nil {
		t.Fatalf("Ps after destroy: %v", err)
	}
	if len(finalStates) != 0 {
		t.Errorf("Ps after destroy = %+v, want no containers left in the project", finalStates)
	}
}

func composeFilesForTest(inst store.Instance) compose.Files {
	if base := compose.FindComposeFile(inst.WorktreeDir); base != "" {
		return compose.Files{Base: base, Override: inst.WorktreeDir + ".compose-override.yml"}
	}
	return compose.Files{Base: inst.WorktreeDir + ".compose-synthesized.yml", Override: inst.WorktreeDir + ".compose-override.yml"}
}

func startForTest(t *testing.T, s *store.Store, params CreateParams, idOrName string) (CreateResult, error) {
	t.Helper()
	return startInstanceWithCmd(context.Background(), s, params, idOrName, false, []string{"sleep", "60"}, nil)
}

// TestCreateInstanceWithSynthesizedServiceNeedsNoHostPort covers the
// no-compose-file path: a .claudio.yml services: entry with no compose
// file of its own must still start the sidecar (internal-only, reached
// by service name — config.Service has no ports field) without
// allocating any host port for it.
func TestCreateInstanceWithSynthesizedServiceNeedsNoHostPort(t *testing.T) {
	dockerComposeAvailable(t)
	s := openTestStore(t)
	// redis:7-alpine, not bare alpine: config.Service has no command
	// field to override an image's default entrypoint (unlike
	// compose.AgentSpec.Cmd), so the synthesized service needs a real
	// image with its own long-running default command to stay up long
	// enough for Ps to observe it running.
	repoURL := newLocalOriginRepoWithClaudioYML(t, "services:\n  - name: cache\n    image: redis:7-alpine\n")

	params := baseCreateParams(t, repoURL)
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	project := compose.ProjectName(result.InstanceID)
	t.Cleanup(func() {
		inst, _ := s.GetInstance(context.Background(), result.InstanceID)
		files := compose.Files{Base: inst.WorktreeDir + ".compose-synthesized.yml", Override: inst.WorktreeDir + ".compose-override.yml"}
		compose.Down(context.Background(), "", project, files, true)
	})

	if len(result.Ports) != 0 {
		t.Errorf("Ports = %+v, want none — a synthesized service is internal-only", result.Ports)
	}

	states, err := compose.Ps(context.Background(), "", project)
	if err != nil {
		t.Fatalf("Ps: %v", err)
	}
	found := false
	for _, s := range states {
		if s.Service == "cache" && s.State == "running" {
			found = true
		}
	}
	if !found {
		t.Errorf("Ps = %+v, want a running cache service", states)
	}
}
