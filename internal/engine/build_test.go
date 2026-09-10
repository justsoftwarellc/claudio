// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package engine

import (
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// testTag returns a unique-per-run tag under a claudio-test/ namespace so
// concurrent/rerun test invocations never collide, and so cleanup can
// remove exactly the image this test built (never someone else's
// claudio/base) — mirrors this project's stance elsewhere (e.g.
// RemoveContainer's doc) on only touching what a test itself created.
func testTag(t *testing.T) string {
	t.Helper()
	return "claudio-test/build-" + strconv.FormatInt(time.Now().UnixNano(), 36) + ":latest"
}

func removeTestImage(t *testing.T, tag string) {
	t.Helper()
	exec.Command("docker", "rmi", "-f", tag).Run()
}

// TestImageExistsFalseForUnknownImage verifies the miss path against a
// real daemon: a tag that was never built or pulled reports false, not
// an error — ImageExists must distinguish "not found" (errdefs.IsNotFound)
// from any other inspect failure.
func TestImageExistsFalseForUnknownImage(t *testing.T) {
	dockerAvailable(t)
	exists, err := ImageExists(context.Background(), "", "claudio-test/definitely-does-not-exist:latest")
	if err != nil {
		t.Fatalf("ImageExists: %v", err)
	}
	if exists {
		t.Fatal("ImageExists = true for an image that was never built or pulled")
	}
}

// TestBuildImageThenImageExists is the real round trip this issue's
// verification checklist asks for: build a tiny image from an in-memory
// Dockerfile, confirm `docker image inspect` (via ImageExists) sees it
// afterward, and that it wasn't visible before the build.
func TestBuildImageThenImageExists(t *testing.T) {
	dockerAvailable(t)
	tag := testTag(t)
	t.Cleanup(func() { removeTestImage(t, tag) })
	ctx := context.Background()

	if exists, err := ImageExists(ctx, "", tag); err != nil {
		t.Fatalf("ImageExists (before build): %v", err)
	} else if exists {
		t.Fatalf("ImageExists = true for %s before it was ever built", tag)
	}

	dockerfile := "FROM alpine:3\nRUN echo hello > /marker\n"
	var progress bytes.Buffer
	err := BuildImage(ctx, "", BuildSpec{
		Files:      []BuildFile{{Name: "Dockerfile", Contents: []byte(dockerfile)}},
		Dockerfile: "Dockerfile",
		Tags:       []string{tag},
		Progress:   &progress,
	})
	if err != nil {
		t.Fatalf("BuildImage: %v (build output: %s)", err, progress.String())
	}
	if progress.Len() == 0 {
		t.Error("BuildImage produced no progress output, want the build log streamed to Progress")
	}

	exists, err := ImageExists(ctx, "", tag)
	if err != nil {
		t.Fatalf("ImageExists (after build): %v", err)
	}
	if !exists {
		t.Fatalf("ImageExists = false for %s right after BuildImage succeeded", tag)
	}

	// The build actually ran the RUN instruction, not just tagged
	// something already present — confirms this exercised a real build,
	// not merely a re-tag.
	out, err := exec.Command("docker", "run", "--rm", tag, "cat", "/marker").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run %s cat /marker: %v: %s", tag, err, out)
	}
	if got := string(bytes.TrimSpace(out)); got != "hello" {
		t.Errorf("/marker contents = %q, want %q", got, "hello")
	}
}

// TestBuildImagePassesBuildArgs verifies build args actually reach the
// Dockerfile (ARG/RUN), the mechanism ROD-96's UID/GID matching depends
// on — checked here at the plumbing level; the UID/GID-specific
// end-to-end check lives in imagebuild's own test against the real base
// Dockerfile.
func TestBuildImagePassesBuildArgs(t *testing.T) {
	dockerAvailable(t)
	tag := testTag(t)
	t.Cleanup(func() { removeTestImage(t, tag) })
	ctx := context.Background()

	dockerfile := "FROM alpine:3\nARG GREETING=default\nRUN echo \"$GREETING\" > /greeting\n"
	err := BuildImage(ctx, "", BuildSpec{
		Files:      []BuildFile{{Name: "Dockerfile", Contents: []byte(dockerfile)}},
		Dockerfile: "Dockerfile",
		Tags:       []string{tag},
		BuildArgs:  map[string]string{"GREETING": "hello-from-build-arg"},
	})
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}

	out, err := exec.Command("docker", "run", "--rm", tag, "cat", "/greeting").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run %s cat /greeting: %v: %s", tag, err, out)
	}
	if got := string(bytes.TrimSpace(out)); got != "hello-from-build-arg" {
		t.Errorf("/greeting contents = %q, want the build-arg value", got)
	}
}

// TestBuildImageFailsOnBrokenDockerfile verifies BuildImage surfaces a
// build failure as a Go error — the case this package's own doc calls
// out empirically: ImageBuild's HTTP call succeeds even when the build
// itself fails, so a bad RUN command must be caught by inspecting the
// JSON message stream, not by the ImageBuild call's own return value.
func TestBuildImageFailsOnBrokenDockerfile(t *testing.T) {
	dockerAvailable(t)
	tag := testTag(t)
	t.Cleanup(func() { removeTestImage(t, tag) })

	dockerfile := "FROM alpine:3\nRUN this-command-does-not-exist-anywhere\n"
	err := BuildImage(context.Background(), "", BuildSpec{
		Files:      []BuildFile{{Name: "Dockerfile", Contents: []byte(dockerfile)}},
		Dockerfile: "Dockerfile",
		Tags:       []string{tag},
	})
	if err == nil {
		t.Fatal("BuildImage: expected an error for a Dockerfile with a failing RUN command")
	}
}

// TestBuildImageIsCachedOnSecondBuild is this issue's "building the same
// image twice is not disastrously slow/broken" check: a second build of
// an unchanged Dockerfile must not be materially slower than the first
// once Docker's own layer cache is warm — this test doesn't assert a
// specific speed threshold (too flaky across machines/CI), but does
// assert the build succeeds a second time and stays fast enough that
// nothing here is deliberately busting the cache (e.g. accidentally
// passing NoCache).
func TestBuildImageIsCachedOnSecondBuild(t *testing.T) {
	dockerAvailable(t)
	tag := testTag(t)
	t.Cleanup(func() { removeTestImage(t, tag) })
	ctx := context.Background()

	// A RUN step slow enough that a cache hit vs. a cache miss is
	// unambiguous without a network-dependent (and thus flaky) image
	// pull: `sleep 2` inside the build takes ~2s on a cache miss, near-0s
	// on a hit.
	dockerfile := "FROM alpine:3\nRUN sleep 2 && echo built > /marker\n"
	spec := BuildSpec{
		Files:      []BuildFile{{Name: "Dockerfile", Contents: []byte(dockerfile)}},
		Dockerfile: "Dockerfile",
		Tags:       []string{tag},
	}

	start := time.Now()
	if err := BuildImage(ctx, "", spec); err != nil {
		t.Fatalf("first BuildImage: %v", err)
	}
	firstDuration := time.Since(start)

	start = time.Now()
	if err := BuildImage(ctx, "", spec); err != nil {
		t.Fatalf("second BuildImage: %v", err)
	}
	secondDuration := time.Since(start)

	t.Logf("first build: %s, second (cached) build: %s", firstDuration, secondDuration)
	if secondDuration >= firstDuration {
		t.Errorf("second build (%s) was not faster than the first (%s) — Docker's layer cache does not appear to be in effect", secondDuration, firstDuration)
	}
}
