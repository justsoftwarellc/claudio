package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/coreerr"
)

// TestEnsureImageAvailableMissingImageMentionsBuildCommand is this
// issue's third verification requirement: `claudio create` against an
// image that doesn't exist yet gives a clear, actionable error naming
// `claudio image build`, not Docker's raw "no such image" text.
func TestEnsureImageAvailableMissingImageMentionsBuildCommand(t *testing.T) {
	dockerAvailable(t)
	err := EnsureImageAvailable(t.Context(), "", "claudio-test/definitely-not-built:latest", "")
	if err == nil {
		t.Fatal("expected an error for an image that doesn't exist")
	}
	if !coreerr.Is(err, coreerr.Unavailable) {
		code, _ := coreerr.CodeOf(err)
		t.Errorf("code = %q, want %q", code, coreerr.Unavailable)
	}
	if !strings.Contains(err.Error(), "claudio image build") {
		t.Errorf("error = %q, want it to mention `claudio image build`", err.Error())
	}
	if strings.Contains(err.Error(), "No such image") {
		t.Errorf("error = %q, still leaks Docker's own raw error text", err.Error())
	}
}

// TestEnsureImageAvailableMissingImageWithRepoHintMentionsRepoFlag covers
// the --repo variant of the same message: when the caller names a repo
// (because the resolved image is that repo's own tag), the hint must
// say --repo, not just the bare command.
func TestEnsureImageAvailableMissingImageWithRepoHintMentionsRepoFlag(t *testing.T) {
	dockerAvailable(t)
	err := EnsureImageAvailable(t.Context(), "", "claudio-test/definitely-not-built:latest", "/path/to/repo")
	if err == nil {
		t.Fatal("expected an error for an image that doesn't exist")
	}
	if !strings.Contains(err.Error(), "claudio image build --repo /path/to/repo") {
		t.Errorf("error = %q, want it to mention `claudio image build --repo /path/to/repo`", err.Error())
	}
}

// TestEnsureImageAvailableExistingImageIsNil is the control case: an
// image that does exist must not error at all.
func TestEnsureImageAvailableExistingImageIsNil(t *testing.T) {
	dockerAvailable(t)
	// alpine is already required to be pullable/available by other tests
	// in this package's own suite (baseCreateParams uses it); pull it
	// explicitly here too so this test doesn't depend on run order.
	if err := exec.Command("docker", "image", "inspect", "alpine").Run(); err != nil {
		if out, pullErr := exec.Command("docker", "pull", "alpine").CombinedOutput(); pullErr != nil {
			t.Skipf("alpine not available locally and could not be pulled: %v: %s", pullErr, out)
		}
	}
	if err := EnsureImageAvailable(t.Context(), "", "alpine", ""); err != nil {
		t.Errorf("EnsureImageAvailable(alpine) = %v, want nil", err)
	}
}

// TestResolveImageExplicitOverrideWins verifies CreateParams.Image, when
// set, is used verbatim and never replaced by a repo-derived tag — this
// issue's constraint that the build path is about the *default*
// resolution, not about taking away the ability to point at an arbitrary
// already-built image.
func TestResolveImageExplicitOverrideWins(t *testing.T) {
	worktreeDir := t.TempDir() // no .claudio.yml here — must not matter when explicit
	image, hint, err := resolveImage("git@github.com:acme/web.git", worktreeDir, "my-custom-image:latest")
	if err != nil {
		t.Fatalf("resolveImage: %v", err)
	}
	if image != "my-custom-image:latest" {
		t.Errorf("image = %q, want the explicit override verbatim", image)
	}
	if hint != "" {
		t.Errorf("imageRepoHint = %q, want empty for an explicit override", hint)
	}
}

// TestResolveImageDefaultsToBaseWithNoImageConfig verifies a repo with no
// .claudio.yml (or one with no image: section) defaults to the base
// image, matching the pre-ROD-96 hardcoded default exactly — no
// behavior change for the common case.
func TestResolveImageDefaultsToBaseWithNoImageConfig(t *testing.T) {
	worktreeDir := t.TempDir()
	image, hint, err := resolveImage("git@github.com:acme/web.git", worktreeDir, "")
	if err != nil {
		t.Fatalf("resolveImage: %v", err)
	}
	if image != "claudio/base:latest" {
		t.Errorf("image = %q, want claudio/base:latest", image)
	}
	if hint != "" {
		t.Errorf("imageRepoHint = %q, want empty when the base image is used", hint)
	}
}

// TestResolveImagePicksRepoTagWhenClaudioYmlAsksForOne verifies the
// wiring this issue's --repo error hint depends on: a repo whose
// .claudio.yml declares apt/npm_global gets the repo-specific tag as its
// *default* image, not the base — otherwise those declared packages
// would never end up in any container the repo actually runs in.
func TestResolveImagePicksRepoTagWhenClaudioYmlAsksForOne(t *testing.T) {
	worktreeDir := t.TempDir()
	claudioYML := filepath.Join(worktreeDir, ".claudio.yml")
	if err := os.WriteFile(claudioYML, []byte("image:\n  apt: [libpq-dev]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repoURL := "git@github.com:acme/web.git"
	image, hint, err := resolveImage(repoURL, worktreeDir, "")
	if err != nil {
		t.Fatalf("resolveImage: %v", err)
	}
	if image == "claudio/base:latest" {
		t.Error("image = claudio/base:latest, want the repo-specific tag since .claudio.yml declares apt packages")
	}
	if !strings.HasPrefix(image, "claudio/repo-") {
		t.Errorf("image = %q, want a claudio/repo-* tag", image)
	}
	if hint != worktreeDir {
		t.Errorf("imageRepoHint = %q, want the worktree dir %q (a --repo path that actually has .claudio.yml)", hint, worktreeDir)
	}
}
