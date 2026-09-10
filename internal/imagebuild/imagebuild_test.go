// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package imagebuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/repo"
)

func TestNeedsRepoImage(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Image
		want bool
	}{
		{"empty", config.Image{}, false},
		{"base only", config.Image{Base: "node:22-slim"}, false}, // base picks the *base* image, not a second layer
		{"apt", config.Image{Apt: []string{"libpq-dev"}}, true},
		{"npm_global", config.Image{NpmGlobal: []string{"pnpm"}}, true},
		{"dockerfile escape hatch", config.Image{Dockerfile: ".claudio/Dockerfile"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NeedsRepoImage(c.cfg); got != c.want {
				t.Errorf("NeedsRepoImage(%+v) = %v, want %v", c.cfg, got, c.want)
			}
		})
	}
}

// TestRepoImageTagReusesRepoSlug pins the tagging scheme to
// internal/repo.Slug's own derivation — the task's requirement that
// different repos with different toolchain needs don't collide on one
// tag, using the *same* derivation create's own repo root already uses
// rather than a second, potentially-diverging scheme.
func TestRepoImageTagReusesRepoSlug(t *testing.T) {
	repoURL := "git@github.com:acme/web.git"
	tag, err := RepoImageTag(repoURL)
	if err != nil {
		t.Fatalf("RepoImageTag: %v", err)
	}
	slug, err := repo.Slug(repoURL)
	if err != nil {
		t.Fatalf("repo.Slug: %v", err)
	}
	want := "claudio/repo-" + slug + ":latest"
	if tag != want {
		t.Errorf("RepoImageTag(%q) = %q, want %q", repoURL, tag, want)
	}
}

// TestRepoImageTagIsLowercase is a regression test for a real bug found
// while writing this package's end-to-end tests: repo.Slug can contain
// uppercase segments (a mixed-case org/repo name, an uppercase local
// path component), and Docker's own API rejects a repository name with
// any uppercase character outright ("invalid reference format ... must
// be lowercase") — RepoImageTag must lowercase the slug itself rather
// than assume repo.Slug already does (it deliberately doesn't: a
// directory name is legitimately case-sensitive on the host, which is
// repo.Slug's actual job).
func TestRepoImageTagIsLowercase(t *testing.T) {
	tag, err := RepoImageTag("git@github.com:Acme/WebApp.git")
	if err != nil {
		t.Fatalf("RepoImageTag: %v", err)
	}
	if tag != strings.ToLower(tag) {
		t.Errorf("RepoImageTag(%q) = %q, contains uppercase (Docker rejects this)", "git@github.com:Acme/WebApp.git", tag)
	}
}

// TestRepoImageTagDiffersAcrossRepos is the concrete "don't collide"
// check: two different repos with different apt/npm_global needs must
// get different tags.
func TestRepoImageTagDiffersAcrossRepos(t *testing.T) {
	tagA, err := RepoImageTag("git@github.com:acme/web.git")
	if err != nil {
		t.Fatalf("RepoImageTag: %v", err)
	}
	tagB, err := RepoImageTag("git@github.com:acme/api.git")
	if err != nil {
		t.Fatalf("RepoImageTag: %v", err)
	}
	if tagA == tagB {
		t.Errorf("RepoImageTag produced the same tag %q for two different repos", tagA)
	}
}

// TestRepoImageTagStableAcrossCalls verifies a second `image build
// --repo` against the same repo lands on the same tag rather than
// accumulating new ones — mirrors repo.Slug's own "deterministic so a
// second create finds the same root" contract.
func TestRepoImageTagStableAcrossCalls(t *testing.T) {
	repoURL := "git@github.com:acme/web.git"
	tag1, err := RepoImageTag(repoURL)
	if err != nil {
		t.Fatalf("RepoImageTag: %v", err)
	}
	tag2, err := RepoImageTag(repoURL)
	if err != nil {
		t.Fatalf("RepoImageTag: %v", err)
	}
	if tag1 != tag2 {
		t.Errorf("RepoImageTag(%q) = %q then %q, want stable across calls", repoURL, tag1, tag2)
	}
}

func TestGenerateRepoDockerfileIncludesAptAndNpmGlobal(t *testing.T) {
	df := string(generateRepoDockerfile("claudio/base:latest", config.Image{
		Apt:       []string{"libpq-dev", "postgresql-client"},
		NpmGlobal: []string{"pnpm"},
	}))
	if !strings.HasPrefix(df, "FROM claudio/base:latest\n") {
		t.Errorf("generated Dockerfile does not start FROM the base image:\n%s", df)
	}
	if !strings.Contains(df, "apt-get install") || !strings.Contains(df, "libpq-dev") || !strings.Contains(df, "postgresql-client") {
		t.Errorf("generated Dockerfile missing apt packages:\n%s", df)
	}
	if !strings.Contains(df, "npm install -g pnpm") {
		t.Errorf("generated Dockerfile missing npm_global install:\n%s", df)
	}
}

func TestGenerateRepoDockerfileOmitsEmptySections(t *testing.T) {
	df := string(generateRepoDockerfile("claudio/base:latest", config.Image{Apt: []string{"libpq-dev"}}))
	if strings.Contains(df, "npm install -g") {
		t.Errorf("generated Dockerfile has an npm install with no npm_global packages configured:\n%s", df)
	}
}

// TestGenerateRepoDockerfileSinglePackageLineContinuation is a
// regression test for a real bug found while writing this package's
// live Docker tests: a single-entry apt list produced a package line
// with no trailing "\", which left the following "&& rm -rf ..." line
// as its own (invalid) Dockerfile instruction — Docker rejected it with
// "unknown instruction: &&". Every generated package line must continue
// the RUN, including the last one.
func TestGenerateRepoDockerfileSinglePackageLineContinuation(t *testing.T) {
	df := string(generateRepoDockerfile("claudio/base:latest", config.Image{Apt: []string{"jq"}}))
	for i, line := range strings.Split(strings.TrimRight(df, "\n"), "\n") {
		if strings.Contains(line, "&& rm -rf /var/lib/apt/lists") {
			continue // the terminating line itself has no continuation
		}
		if strings.Contains(line, "apt-get update") || strings.TrimSpace(line) == "jq" {
			if !strings.HasSuffix(line, `\`) {
				t.Errorf("line %d (%q) is part of a multi-line RUN but has no trailing continuation", i, line)
			}
		}
	}
}

// TestResolveEscapeHatchExplicitFieldWins verifies image.dockerfile in
// .claudio.yml is authoritative over the conventional
// .claudio/Dockerfile path, per this package's own doc: "an explicit
// config value always beats an implicit convention."
func TestResolveEscapeHatchExplicitFieldWins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "custom.Dockerfile"), "FROM claudio/base:latest\nRUN echo explicit\n")
	writeFile(t, filepath.Join(dir, ".claudio", "Dockerfile"), "FROM claudio/base:latest\nRUN echo conventional\n")

	path, contents, err := resolveEscapeHatch(dir, config.Image{Dockerfile: "custom.Dockerfile"})
	if err != nil {
		t.Fatalf("resolveEscapeHatch: %v", err)
	}
	if path != "custom.Dockerfile" {
		t.Errorf("path = %q, want %q", path, "custom.Dockerfile")
	}
	if !strings.Contains(string(contents), "explicit") {
		t.Errorf("contents = %q, want the explicit file's contents", contents)
	}
}

func TestResolveEscapeHatchFallsBackToConventionalClaudioDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".claudio", "Dockerfile"), "FROM claudio/base:latest\nRUN echo conventional\n")

	path, contents, err := resolveEscapeHatch(dir, config.Image{})
	if err != nil {
		t.Fatalf("resolveEscapeHatch: %v", err)
	}
	if path != filepath.Join(".claudio", "Dockerfile") {
		t.Errorf("path = %q, want %q", path, filepath.Join(".claudio", "Dockerfile"))
	}
	if !strings.Contains(string(contents), "conventional") {
		t.Errorf("contents = %q, want the conventional file's contents", contents)
	}
}

func TestResolveEscapeHatchFallsBackToDevcontainer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".devcontainer", "Dockerfile"), "FROM claudio/base:latest\nRUN echo devcontainer\n")

	path, contents, err := resolveEscapeHatch(dir, config.Image{})
	if err != nil {
		t.Fatalf("resolveEscapeHatch: %v", err)
	}
	if path != filepath.Join(".devcontainer", "Dockerfile") {
		t.Errorf("path = %q, want %q", path, filepath.Join(".devcontainer", "Dockerfile"))
	}
	if !strings.Contains(string(contents), "devcontainer") {
		t.Errorf("contents = %q, want the devcontainer file's contents", contents)
	}
}

func TestResolveEscapeHatchNoneFound(t *testing.T) {
	dir := t.TempDir()
	path, contents, err := resolveEscapeHatch(dir, config.Image{})
	if err != nil {
		t.Fatalf("resolveEscapeHatch: %v", err)
	}
	if path != "" || contents != nil {
		t.Errorf("resolveEscapeHatch(no files present) = (%q, %v), want (\"\", nil)", path, contents)
	}
}

// TestResolveEscapeHatchExplicitPathMissingIsAnError verifies an
// explicit image.dockerfile that doesn't exist is a config error rather
// than silently falling through to auto-detection — a typo in that path
// should say so, matching this repo's general "unknown key is an error"
// config discipline (internal/config's own package doc).
func TestResolveEscapeHatchExplicitPathMissingIsAnError(t *testing.T) {
	dir := t.TempDir()
	_, _, err := resolveEscapeHatch(dir, config.Image{Dockerfile: "does-not-exist/Dockerfile"})
	if err == nil {
		t.Fatal("resolveEscapeHatch: expected an error for a missing explicit image.dockerfile path")
	}
}

// TestWriterFuncNilProgressIsGenuinelyNil guards against a real bug
// found while writing this package's live tests: returning a typed-nil
// *lineWriter (rather than a bare nil io.Writer) for a nil progress func
// gets boxed into a non-nil io.Writer interface value, which passes
// engine.BuildSpec's own `if spec.Progress == nil` check and then panics
// on the first Write inside jsonmessage.DisplayJSONMessagesStream. A
// nil-interface comparison is the only way to catch this at the call
// site, so this test asserts on that directly rather than exercising a
// real build.
func TestWriterFuncNilProgressIsGenuinelyNil(t *testing.T) {
	w := writerFunc(nil)
	if w != nil {
		t.Fatalf("writerFunc(nil) = %#v, want a genuinely nil io.Writer", w)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
