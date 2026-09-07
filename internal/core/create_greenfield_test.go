package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCreateInstanceGreenfield verifies `claudio create --new <name>`
// (docs/architecture.md §5.1): no upstream repo, git-init a fresh root
// instead, with a worktree added off whatever default branch InitRoot's
// initial commit landed on.
func TestCreateInstanceGreenfield(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)

	params := CreateParams{
		GreenfieldName: "market-research",
		WorkspaceRoot:  t.TempDir(),
		Image:          "alpine",
		Env:            map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "test-token"},
		PortRangeLow:   43000,
		PortRangeHigh:  43999,
	}
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	if result.Branch == "" {
		t.Error("Branch is empty, want the default branch InitRoot's commit landed on")
	}

	if _, err := os.Stat(filepath.Join(result.WorktreeDir, ".git")); err != nil {
		t.Errorf("worktree %s should be a valid git worktree: %v", result.WorktreeDir, err)
	}

	// The worktree should have the initial empty commit from InitRoot,
	// proving it's actually checked out against real history and not an
	// empty/detached directory.
	cmd := exec.Command("git", "log", "--oneline")
	cmd.Dir = result.WorktreeDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log in worktree: %v: %s", err, out)
	}
	if len(out) == 0 {
		t.Error("git log in the greenfield worktree is empty, want at least InitRoot's initial commit")
	}

	inst, err := s.GetInstance(t.Context(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.RepoURL == "" {
		t.Error("RepoURL is empty, want a synthetic value identifying the greenfield root")
	}
}

func TestCreateInstanceGreenfieldTwiceWithSameNameErrors(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	workspaceRoot := t.TempDir()

	params := CreateParams{
		GreenfieldName: "market-research",
		WorkspaceRoot:  workspaceRoot,
		Image:          "alpine",
		Env:            map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "test-token"},
		PortRangeLow:   43000,
		PortRangeHigh:  43999,
	}
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance (1st): %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	_, err = createForTest(t, s, params)
	if err == nil {
		t.Fatal("expected an error creating a second greenfield instance with the same --new name (repo.InitRoot refuses an existing root)")
	}
}
