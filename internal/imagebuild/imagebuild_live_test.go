// Tests in this file build real images against a real Docker daemon —
// this package's own verification discipline (see docs/architecture.md's
// stance on checking infra claims empirically, not assuming them): a
// Dockerfile that merely *looks* like it applies apt/npm_global/UID
// matching correctly is not the same as one that has been built and
// exec'd into to confirm it actually does. Run with `go test ./... -p 1`
// — see internal/engine/create_test.go's package comment for why
// parallel package runs are flaky against a shared Docker daemon.
package imagebuild

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rodrigomorales/claudio/internal/config"
)

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

func uniqueTag(prefix string) string {
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) + ":latest"
}

func removeImage(t *testing.T, tag string) {
	t.Helper()
	exec.Command("docker", "rmi", "-f", tag).Run()
}

// TestBuildBaseProducesInspectableImageWithMatchingUIDGID is this issue's
// primary empirical verification requirement: `claudio image build`
// actually produces an image `docker image inspect` can see, with the
// UID/GID matching the *real* host user — checked by execing into a
// container from it and running `id`, not by reading the Dockerfile text.
//
// Builds under a throwaway tag (BuildBase always tags BaseImage itself;
// this test can't safely build straight to that shared tag without
// racing/clobbering a real claudio/base:latest a developer might already
// have), by calling engine.BuildImage directly with the same embedded
// Files BuildBase uses — see the tag-collision comment inline below for
// why BuildBase itself isn't called here.
func TestBuildBaseProducesInspectableImageWithMatchingUIDGID(t *testing.T) {
	dockerAvailable(t)
	tag := uniqueTag("claudio-test/base")
	t.Cleanup(func() { removeImage(t, tag) })

	wantUID := os.Getuid()
	wantGID := os.Getgid()
	buildArgs := BuildArgsForHost(wantUID, wantGID)
	if buildArgs["USER_UID"] != strconv.Itoa(wantUID) || buildArgs["USER_GID"] != strconv.Itoa(wantGID) {
		t.Fatalf("BuildArgsForHost(%d, %d) = %v, want the real host values", wantUID, wantGID, buildArgs)
	}

	if err := buildBaseAs(context.Background(), tag, buildArgs); err != nil {
		t.Fatalf("build base image: %v", err)
	}

	// docker image inspect, verbatim — the task's own verification
	// wording ("with the UID/GID matching the real host user ... verify
	// id -u/id -g on the test machine match what ends up in the built
	// image").
	if err := exec.Command("docker", "image", "inspect", tag).Run(); err != nil {
		t.Fatalf("docker image inspect %s: %v (image not visible after build)", tag, err)
	}

	// --entrypoint overrides the image's own tini+entrypoint.sh chain,
	// which otherwise refuses to run at all without a credential (see
	// image/entrypoint.sh) — this is a UID/GID probe, not a real
	// provisioning run, so it needs `id` to actually be the container's
	// command rather than an argument tini never reaches.
	out, err := exec.Command("docker", "run", "--rm", "--user", "agent", "--entrypoint", "id", tag, "-u").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run %s id -u: %v: %s", tag, err, out)
	}
	gotUID := strings.TrimSpace(string(out))
	if gotUID != strconv.Itoa(wantUID) {
		t.Errorf("container id -u = %q, want %d (the real host UID)", gotUID, wantUID)
	}

	out, err = exec.Command("docker", "run", "--rm", "--user", "agent", "--entrypoint", "id", tag, "-g").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run %s id -g: %v: %s", tag, err, out)
	}
	gotGID := strings.TrimSpace(string(out))
	if gotGID != strconv.Itoa(wantGID) {
		t.Errorf("container id -g = %q, want %d (the real host GID)", gotGID, wantGID)
	}
}

// TestBuildBaseTwiceIsFast is the base-image half of "building the same
// image twice is not disastrously slow/broken" — the apt-get/npm install
// layers should be fully cached on a second build.
func TestBuildBaseTwiceIsFast(t *testing.T) {
	dockerAvailable(t)
	tag := uniqueTag("claudio-test/base-cache")
	t.Cleanup(func() { removeImage(t, tag) })
	buildArgs := BuildArgsForHost(os.Getuid(), os.Getgid())

	start := time.Now()
	if err := buildBaseAs(context.Background(), tag, buildArgs); err != nil {
		t.Fatalf("first build: %v", err)
	}
	first := time.Since(start)

	start = time.Now()
	if err := buildBaseAs(context.Background(), tag, buildArgs); err != nil {
		t.Fatalf("second build: %v", err)
	}
	second := time.Since(start)

	t.Logf("first build: %s, second (cached) build: %s", first, second)

	// An absolute bound, not `second < first`. When Docker's layer cache
	// is already warm from an earlier run, the "first" build here is
	// itself fully cached: both builds are then a couple hundred
	// milliseconds of cache-lookup overhead, and comparing them measures
	// scheduling noise rather than caching — the assertion failed roughly
	// half the time on a developer machine that had built before. What the
	// test means to catch is a second build that redoes the apt-get/npm
	// work, which takes tens of seconds and no amount of noise reaches.
	const cachedBuildCeiling = 10 * time.Second
	if second > cachedBuildCeiling {
		t.Errorf("second base-image build took %s (first: %s), over the %s ceiling — layers are not being cached",
			second, first, cachedBuildCeiling)
	}
}

// TestBuildRepoImageInstallsAptAndNpmGlobal is this issue's second
// empirical requirement: a .claudio.yml with apt/npm_global entries
// actually results in those packages being present in the generated
// per-repo image — checked by execing in, not by asserting the
// generated Dockerfile text looks right.
func TestBuildRepoImageInstallsAptAndNpmGlobal(t *testing.T) {
	dockerAvailable(t)
	baseTag := uniqueTag("claudio-test/base-for-repo")
	t.Cleanup(func() { removeImage(t, baseTag) })
	if err := buildBaseAs(context.Background(), baseTag, BuildArgsForHost(os.Getuid(), os.Getgid())); err != nil {
		t.Fatalf("build base: %v", err)
	}

	repoPath := t.TempDir()
	repoURL := "git@github.com:acme/imagebuild-apt-test.git"
	cfg := config.Image{
		Apt:       []string{"jq"},
		NpmGlobal: []string{"is-odd"}, // tiny, no native deps — fast to install and easy to verify with `npm ls -g`
	}

	tag, err := BuildRepoImage(context.Background(), "", repoURL, repoPath, baseTag, cfg, nil, nil)
	if err != nil {
		t.Fatalf("BuildRepoImage: %v", err)
	}
	t.Cleanup(func() { removeImage(t, tag) })

	wantTag, err := RepoImageTag(repoURL)
	if err != nil {
		t.Fatalf("RepoImageTag: %v", err)
	}
	if tag != wantTag {
		t.Errorf("BuildRepoImage tag = %q, want %q", tag, wantTag)
	}

	// --entrypoint overrides the image's own tini+entrypoint.sh chain — see
	// TestBuildBaseProducesInspectableImageWithMatchingUIDGID's comment on
	// why: it refuses to run at all without a credential otherwise.
	if out, err := exec.Command("docker", "run", "--rm", "--entrypoint", "jq", tag, "--version").CombinedOutput(); err != nil {
		t.Errorf("jq not present in built image: %v: %s", err, out)
	}
	out, err := exec.Command("docker", "run", "--rm", "--entrypoint", "npm", tag, "ls", "-g", "--depth=0").CombinedOutput()
	if err != nil {
		t.Fatalf("npm ls -g in built image: %v: %s", err, out)
	}
	if !bytes.Contains(out, []byte("is-odd")) {
		t.Errorf("npm_global package is-odd not present in built image; npm ls -g output:\n%s", out)
	}
}

// TestBuildRepoImageEscapeHatchReplacesGeneratedLayer verifies §7.1's
// escape-hatch semantics: a .claudio/Dockerfile is used AS the entire
// repo-layer Dockerfile (must itself FROM the base), not layered
// alongside a generated apt/npm_global Dockerfile — a repo declaring
// both an escape hatch and apt packages gets only what the escape hatch
// itself installs.
func TestBuildRepoImageEscapeHatchReplacesGeneratedLayer(t *testing.T) {
	dockerAvailable(t)
	baseTag := uniqueTag("claudio-test/base-for-escapehatch")
	t.Cleanup(func() { removeImage(t, baseTag) })
	if err := buildBaseAs(context.Background(), baseTag, BuildArgsForHost(os.Getuid(), os.Getgid())); err != nil {
		t.Fatalf("build base: %v", err)
	}

	repoPath := t.TempDir()
	escapeHatchDir := filepath.Join(repoPath, ".claudio")
	if err := os.MkdirAll(escapeHatchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dockerfile := "FROM " + baseTag + "\nUSER root\nRUN echo from-escape-hatch > /escape-hatch-marker\nUSER agent\n"
	if err := os.WriteFile(filepath.Join(escapeHatchDir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}

	repoURL := "git@github.com:acme/imagebuild-escapehatch-test.git"
	// apt is set too, to prove it's ignored once the escape hatch applies
	// (per §7.1: a full custom Dockerfile the repo controls end to end,
	// not a fragment Claudio appends to).
	cfg := config.Image{Apt: []string{"jq"}}

	tag, err := BuildRepoImage(context.Background(), "", repoURL, repoPath, baseTag, cfg, nil, nil)
	if err != nil {
		t.Fatalf("BuildRepoImage: %v", err)
	}
	t.Cleanup(func() { removeImage(t, tag) })

	// --entrypoint overrides the image's own tini+entrypoint.sh chain —
	// see TestBuildBaseProducesInspectableImageWithMatchingUIDGID's
	// comment on why: it refuses to run at all without a credential
	// otherwise.
	out, err := exec.Command("docker", "run", "--rm", "--entrypoint", "cat", tag, "/escape-hatch-marker").CombinedOutput()
	if err != nil {
		t.Fatalf("escape-hatch marker not present: %v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "from-escape-hatch" {
		t.Errorf("/escape-hatch-marker = %q, want %q", got, "from-escape-hatch")
	}

	if out, err := exec.Command("docker", "run", "--rm", "--entrypoint", "which", tag, "jq").CombinedOutput(); err == nil {
		t.Errorf("jq present in image (apt should have been ignored once the escape hatch applied): %s", out)
	}
}

// buildBaseAs builds the real embedded base Dockerfile under a
// throwaway tag via buildBaseTagged, BuildBase's own tag-parameterized
// implementation — see that function's doc for why tests need this
// instead of calling BuildBase itself (which always targets the shared
// BaseImage tag).
func buildBaseAs(ctx context.Context, tag string, buildArgs map[string]string) error {
	return buildBaseTagged(ctx, "", tag, buildArgs, nil)
}
