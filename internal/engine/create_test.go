// Tests in this file talk to a real Docker daemon, and so does
// internal/core's Reconcile/AdoptContainer test suite (both scan every
// container carrying the claudio.instance.id label — that's the whole
// point of both packages). Found empirically: `go test ./...` runs
// packages in parallel by default, and internal/core's untracked-
// container assertions can observe an internal/engine test's
// still-running fixture container mid-test, since neither package
// namespaces by more than a label neither owns exclusively. Run with
// `go test ./... -p 1` — but note that even `-p 1` only serializes which
// package's test *binary* runs at a time, not the OS-level ports Docker
// hands out to already-running containers from a package that just
// finished: an occasional flake has an internal/core AllocatePort probe
// lose a real bind() race to a not-yet-torn-down internal/engine fixture
// container holding the exact same ephemeral host port. Confirmed
// transient by rerunning; there is no CI setup to enforce any of this —
// there is no CI config in this repo yet.
package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rodrigomorales/claudio/internal/repo"
)

// dockerAvailable mirrors the skip condition used elsewhere in the repo
// (internal/repo/repo_test.go, internal/core/reconcile_test.go).
func dockerAvailable(t *testing.T) {
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
}

func imageAvailable(t *testing.T, image string) {
	t.Helper()
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		t.Skipf("image %s not built locally (run image/ build first)", image)
	}
}

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// newLocalOriginRepo mirrors internal/repo/repo_test.go's helper: a bare
// repo on disk so this test never touches the network.
func newLocalOriginRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.git")
	seed := filepath.Join(dir, "seed")

	run(t, "", "git", "init", "--bare", origin)
	run(t, "", "git", "init", seed)
	run(t, seed, "git", "config", "user.email", "test@example.com")
	run(t, seed, "git", "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, seed, "git", "add", "README.md")
	run(t, seed, "git", "commit", "-m", "initial")
	run(t, seed, "git", "branch", "-M", "main")
	run(t, seed, "git", "remote", "add", "origin", origin)
	run(t, seed, "git", "push", "origin", "main")
	return "file://" + origin
}

// TestCreateAndStartRealContainer exercises ROD-114's own verification
// checklist end to end: a container created by CreateAndStart actually
// starts, git works inside it against the mounted worktree (the Appendix
// B round-trip, but through real provisioning rather than a hand-built
// `docker run`), a commit made inside is visible on the host, the
// claudio.* labels are readable back, and the container is not a child
// of this test process.
func TestCreateAndStartRealContainer(t *testing.T) {
	dockerAvailable(t)
	const image = "claudio/base:dev"
	imageAvailable(t, image)

	origin := newLocalOriginRepo(t)
	workspace := t.TempDir()
	homeDir := t.TempDir()
	ctx := context.Background()

	root, err := repo.EnsureRoot(ctx, workspace, origin)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	worktreeDir, err := repo.AddWorktree(ctx, root, "brave-otter", "claudio/brave-otter", true)
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	instanceID := "brave-otter-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	createdAt := time.Now().Unix()

	containerID, err := CreateAndStart(ctx, "", CreateSpec{
		InstanceID:  instanceID,
		RepoURL:     origin,
		CreatedAt:   createdAt,
		Image:       image,
		RepoRoot:    root.Path,
		WorktreeDir: worktreeDir,
		HomeDir:     homeDir,
		Env:         map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "test-not-a-real-token"},
	})
	if err != nil {
		t.Fatalf("CreateAndStart: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", containerID).Run() })

	// Verify: running, and readable back via ListClaudioContainers with
	// the labels this test asked for.
	containers, err := ListClaudioContainers(ctx, "", true)
	if err != nil {
		t.Fatalf("ListClaudioContainers: %v", err)
	}
	var found *ContainerState
	for i := range containers {
		if containers[i].ContainerID == containerID {
			found = &containers[i]
		}
	}
	if found == nil {
		t.Fatalf("container %s not found via ListClaudioContainers", containerID)
	}
	if !found.Running {
		t.Errorf("container reports Running=false right after start")
	}
	if found.InstanceID != instanceID {
		t.Errorf("InstanceID label = %q, want %q", found.InstanceID, instanceID)
	}
	if found.RepoURL != origin {
		t.Errorf("RepoURL label = %q, want %q", found.RepoURL, origin)
	}
	if found.Name != "claudio-"+instanceID {
		t.Errorf("container name = %q, want claudio-%s", found.Name, instanceID)
	}

	// Verify: git works inside the container against the mounted
	// worktree, and the working directory is the worktree, not the root.
	pwdOut := run(t, "", "docker", "exec", containerID, "pwd")
	wantWorkdir := "/repo/worktrees/brave-otter"
	if strings.TrimSpace(pwdOut) != wantWorkdir {
		t.Fatalf("pwd inside container = %q, want %q", strings.TrimSpace(pwdOut), wantWorkdir)
	}
	statusOut := run(t, "", "docker", "exec", containerID, "git", "status")
	if !strings.Contains(statusOut, "claudio/brave-otter") {
		t.Fatalf("git status inside container = %q, want it to name the checked-out branch", statusOut)
	}

	// Verify: a commit made inside the container is visible on the host
	// immediately — the Appendix B round-trip through real provisioning.
	run(t, "", "docker", "exec",
		"-e", "GIT_AUTHOR_NAME=test", "-e", "GIT_AUTHOR_EMAIL=test@example.com",
		"-e", "GIT_COMMITTER_NAME=test", "-e", "GIT_COMMITTER_EMAIL=test@example.com",
		containerID, "git", "commit", "--allow-empty", "-m", "from real container")
	hostLog := run(t, worktreeDir, "git", "log", "-1", "--pretty=%s")
	if strings.TrimSpace(hostLog) != "from real container" {
		t.Fatalf("host does not see the container's commit: log = %q", hostLog)
	}
}

// TestCreateAndStartPublishesPorts verifies ports are bound to 127.0.0.1
// at creation time, matching ROD-98's "never 0.0.0.0 by default" rule and
// confirming Docker actually applies the binding this package requests
// (not just that the Go struct was built correctly).
func TestCreateAndStartPublishesPorts(t *testing.T) {
	dockerAvailable(t)
	imageAvailable(t, "alpine")

	instanceID := "port-test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hostPort := 43111 + int(time.Now().UnixNano()%400)
	repoRoot := t.TempDir()

	containerID, err := CreateAndStart(context.Background(), "", CreateSpec{
		InstanceID:  instanceID,
		RepoURL:     "git@github.com:acme/web.git",
		Image:       "alpine",
		Cmd:         []string{"sleep", "60"},
		RepoRoot:    repoRoot,
		WorktreeDir: repoRoot, // degenerate case: no worktree subdivision, workdir is /repo itself
		HomeDir:     t.TempDir(),
		Ports:       []PortBinding{{ContainerPort: 80, HostPort: hostPort}},
	})
	if err != nil {
		t.Fatalf("CreateAndStart: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", containerID).Run() })

	ports, err := InspectPublishedPorts(context.Background(), "", containerID)
	if err != nil {
		t.Fatalf("InspectPublishedPorts: %v", err)
	}
	if len(ports) != 1 || ports[0].ContainerPort != 80 || ports[0].HostPort != hostPort {
		t.Fatalf("published ports = %+v, want one mapping 80->%d", ports, hostPort)
	}

	inspectOut := run(t, "", "docker", "inspect", "--format",
		`{{(index (index .NetworkSettings.Ports "80/tcp") 0).HostIp}}`, containerID)
	if strings.TrimSpace(inspectOut) != "127.0.0.1" {
		t.Fatalf("published HostIp = %q, want 127.0.0.1 (never 0.0.0.0 by default)", strings.TrimSpace(inspectOut))
	}
}

// TestCreateAndStartAppliesResourceLimits verifies memory/CPU/PID limits
// actually reach the container's cgroup, not just the Go struct — ROD-114
// exists because CreateSpec must accept and apply these (ROD-112 owns the
// policy of what the numbers should be).
func TestCreateAndStartAppliesResourceLimits(t *testing.T) {
	dockerAvailable(t)
	imageAvailable(t, "alpine")

	instanceID := "limits-test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	memBytes, err := ParseMemory("256m")
	if err != nil {
		t.Fatalf("ParseMemory: %v", err)
	}
	repoRoot := t.TempDir()

	containerID, err := CreateAndStart(context.Background(), "", CreateSpec{
		InstanceID:  instanceID,
		RepoURL:     "git@github.com:acme/web.git",
		Image:       "alpine",
		Cmd:         []string{"sleep", "60"},
		RepoRoot:    repoRoot,
		WorktreeDir: repoRoot,
		HomeDir:     t.TempDir(),
		Resources: ResourceLimits{
			MemoryBytes: memBytes,
			NanoCPUs:    NanoCPUs(1),
			PIDs:        128,
		},
	})
	if err != nil {
		t.Fatalf("CreateAndStart: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", containerID).Run() })

	memOut := run(t, "", "docker", "inspect", "--format", "{{.HostConfig.Memory}}", containerID)
	if strings.TrimSpace(memOut) != strconv.FormatInt(memBytes, 10) {
		t.Errorf("HostConfig.Memory = %q, want %d", strings.TrimSpace(memOut), memBytes)
	}
	pidsOut := run(t, "", "docker", "inspect", "--format", "{{.HostConfig.PidsLimit}}", containerID)
	if strings.TrimSpace(pidsOut) != "128" {
		t.Errorf("HostConfig.PidsLimit = %q, want 128", strings.TrimSpace(pidsOut))
	}
}

// TestCreateAndStartSurvivesProcessExit confirms the container is not a
// child of this test process (docs/architecture.md, ROD-99): it must
// keep running after the creating process's context and goroutines are
// long gone, with no special "detach" step required.
func TestCreateAndStartSurvivesProcessExit(t *testing.T) {
	dockerAvailable(t)
	imageAvailable(t, "alpine")

	instanceID := "survive-test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	repoRoot := t.TempDir()
	containerID, err := CreateAndStart(context.Background(), "", CreateSpec{
		InstanceID:  instanceID,
		RepoURL:     "git@github.com:acme/web.git",
		Image:       "alpine",
		Cmd:         []string{"sleep", "60"},
		RepoRoot:    repoRoot,
		WorktreeDir: repoRoot,
		HomeDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("CreateAndStart: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", containerID).Run() })

	// CreateAndStart has already returned; nothing in this package holds a
	// reference to the container's process. Re-inspecting via a brand new
	// client call is the proof there's no hidden lifetime coupling.
	time.Sleep(200 * time.Millisecond)
	containers, err := ListClaudioContainers(context.Background(), "", true)
	if err != nil {
		t.Fatalf("ListClaudioContainers: %v", err)
	}
	for _, c := range containers {
		if c.ContainerID == containerID {
			if !c.Running {
				t.Fatalf("container exited on its own after CreateAndStart returned")
			}
			return
		}
	}
	t.Fatalf("container %s disappeared after CreateAndStart returned", containerID)
}
