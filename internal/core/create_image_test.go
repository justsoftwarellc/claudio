package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newLocalOriginRepoWithClaudioYML mirrors newLocalOriginRepo but commits
// a .claudio.yml before pushing, so it's present in the worktree at the
// point CreateInstance/provisionContainer read it — unlike
// create_postcreate_test.go's trick of writing it after the fact (which
// works there because runPostCreate is called directly against an
// already-provisioned worktree, but resolveImage needs the file to exist
// *before* provisionContainer runs, since CreateInstance itself fails
// before reaching runPostCreate's separate load).
func newLocalOriginRepoWithClaudioYML(t *testing.T, claudioYML string) string {
	t.Helper()
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.git")
	seed := filepath.Join(dir, "seed")

	runGit(t, "", "init", "--bare", origin)
	runGit(t, "", "init", seed)
	runGit(t, seed, "config", "user.email", "test@example.com")
	runGit(t, seed, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, ".claudio.yml"), []byte(claudioYML), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "README.md", ".claudio.yml")
	runGit(t, seed, "commit", "-m", "initial")
	runGit(t, seed, "branch", "-M", "main")
	runGit(t, seed, "remote", "add", "origin", origin)
	runGit(t, seed, "push", "origin", "main")
	return "file://" + origin
}

// TestCreateInstanceMissingRepoImageGivesActionableError is this issue's
// end-to-end version of TestResolveImagePicksRepoTagWhenClaudioYmlAsksForOne
// and TestEnsureImageAvailableMissingImageWithRepoHintMentionsRepoFlag:
// a real CreateInstance call against a repo whose .claudio.yml declares
// an image: section (so its default image is the repo-specific tag, which
// was obviously never built in this test) must fail with the friendly
// "claudio image build --repo" message, not Docker's raw "no such image"
// text, and must not have attempted to start a container at all (no
// container ID ever recorded).
func TestCreateInstanceMissingRepoImageGivesActionableError(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepoWithClaudioYML(t, "image:\n  apt: [jq]\n")

	params := baseCreateParams(t, repoURL)
	params.Image = "" // no override: resolveImage must pick the (unbuilt) repo-specific tag

	_, err := CreateInstance(t.Context(), s, params, nil)
	if err == nil {
		t.Fatal("expected CreateInstance to fail: the repo-specific image was never built")
	}
	if !strings.Contains(err.Error(), "claudio image build --repo") {
		t.Errorf("error = %q, want it to mention `claudio image build --repo`", err.Error())
	}
	if strings.Contains(err.Error(), "No such image") {
		t.Errorf("error = %q, still leaks Docker's own raw error text", err.Error())
	}

	instances, listErr := s.ListInstances(t.Context())
	if listErr != nil {
		t.Fatalf("ListInstances: %v", listErr)
	}
	if len(instances) != 1 {
		t.Fatalf("len(instances) = %d, want 1", len(instances))
	}
	if instances[0].ContainerID != nil {
		t.Errorf("ContainerID = %v, want nil — no container should have been created once the image check failed", instances[0].ContainerID)
	}
}

// TestCreateInstanceDefaultsToBaseImageWithNoClaudioYML is the control
// case for the test above: a repo with no .claudio.yml image: section
// still resolves to claudio/base:latest, matching this project's
// existing behavior before ROD-96's build path existed.
//
// Whether CreateInstance succeeds or fails here depends on whether
// claudio/base:latest happens to be built on the machine running this
// test — both are valid outcomes; what matters is which image
// resolveImage picked. But a real claudio/base:latest commonly IS
// present (e.g. right after `claudio image build`, or after this
// package's own other tests build it), in which case CreateInstance
// really does start a real, long-lived container — found the hard way,
// an earlier version of this test left three such containers running
// after a single `go test ./...`, which then made
// internal/core/reconcile_test.go's untracked-container assertions fail
// by seeing them. This version always cleans up whatever container
// (real or none) CreateInstance produced.
func TestCreateInstanceDefaultsToBaseImageWithNoClaudioYML(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	params := baseCreateParams(t, repoURL)
	params.Image = ""

	result, err := CreateInstance(t.Context(), s, params, nil)
	if err == nil {
		t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })
		return
	}
	if strings.Contains(err.Error(), "claudio/repo-") {
		t.Errorf("error = %q, resolved a repo-specific tag with no .claudio.yml image: section present", err.Error())
	}
}
