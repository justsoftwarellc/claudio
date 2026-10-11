// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"os/exec"
	"testing"
)

// isolateGitHome points git's --global config at a per-test file via
// GIT_CONFIG_GLOBAL, so `git config --global` writes don't clobber the
// operator's own ~/.gitconfig on the test machine. Returns the file
// path in case a caller needs it. GIT_CONFIG_GLOBAL is git's own
// documented escape hatch for exactly this; HOME-redirection was
// tried first and ran into git's "could not write config file" when
// git normalised an empty-string env var to the empty path.
func isolateGitHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/.gitconfig"
	t.Setenv("GIT_CONFIG_GLOBAL", path)
	return path
}

// clearGitHubEnv isolates githubEnv/credentialEnv from whatever the test
// process's own environment (or a prior subtest) happens to carry, and
// from a `gh` the test machine has installed and logged into — without
// this, TestGithubEnv's "neither var set" case would silently pass by
// falling through to the real host's `gh auth token` instead of
// exercising the nil path.
func clearGitHubEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("PATH", "")
}

func TestGithubEnvPrefersGHToken(t *testing.T) {
	clearGitHubEnv(t)
	t.Setenv("GH_TOKEN", "from-gh-token")
	t.Setenv("GITHUB_TOKEN", "from-github-token")

	got := githubEnv()
	if got["GH_TOKEN"] != "from-gh-token" {
		t.Errorf("githubEnv()[GH_TOKEN] = %q, want %q (GH_TOKEN must win over GITHUB_TOKEN)", got["GH_TOKEN"], "from-gh-token")
	}
}

func TestGithubEnvFallsBackToGitHubToken(t *testing.T) {
	clearGitHubEnv(t)
	t.Setenv("GITHUB_TOKEN", "from-github-token")

	got := githubEnv()
	// Normalized to GH_TOKEN regardless of which var supplied it, since
	// that's the one name image/entrypoint.sh's `gh auth setup-git`
	// step and git's credential helper actually read.
	if got["GH_TOKEN"] != "from-github-token" {
		t.Errorf("githubEnv()[GH_TOKEN] = %q, want %q", got["GH_TOKEN"], "from-github-token")
	}
}

func TestGithubEnvNilWhenNoTokenAndNoGH(t *testing.T) {
	clearGitHubEnv(t) // PATH cleared too, so `gh` can't be found even if installed

	got := githubEnv()
	if got != nil {
		t.Errorf("githubEnv() = %v, want nil when no token is set and `gh` isn't on PATH", got)
	}
}

func TestGithubEnvFallsBackToGhAuthToken(t *testing.T) {
	clearGitHubEnv(t)
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh not installed on this machine")
	}
	if err := exec.Command("gh", "auth", "token").Run(); err != nil {
		t.Skip("gh is installed but not logged in")
	}
	// Restore PATH so `gh` is reachable, but leave both token vars unset.
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	got := githubEnv()
	if got["GH_TOKEN"] == "" {
		t.Error("githubEnv()[GH_TOKEN] is empty, want the token `gh auth token` reports")
	}
}

// credentialEnv's own required-credential behavior (fails without an
// Anthropic token) is exercised by the create/start/restart/rebuild
// command tests already; this covers only what's new here — that a
// GitHub token present in the environment rides along, and that its
// absence doesn't fail the command the way a missing Anthropic
// credential does.
func TestCredentialEnvMergesGithubToken(t *testing.T) {
	clearGitHubEnv(t)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "anthropic-token")
	t.Setenv("GH_TOKEN", "gh-token")

	env, ok := credentialEnv("test")
	if !ok {
		t.Fatal("credentialEnv() ok = false, want true with an Anthropic token set")
	}
	if env["GH_TOKEN"] != "gh-token" {
		t.Errorf("credentialEnv()[GH_TOKEN] = %q, want %q", env["GH_TOKEN"], "gh-token")
	}
	if env["CLAUDE_CODE_OAUTH_TOKEN"] != "anthropic-token" {
		t.Errorf("credentialEnv()[CLAUDE_CODE_OAUTH_TOKEN] = %q, want %q", env["CLAUDE_CODE_OAUTH_TOKEN"], "anthropic-token")
	}
}

func TestCredentialEnvOKWithNoGithubToken(t *testing.T) {
	clearGitHubEnv(t)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "anthropic-token")

	env, ok := credentialEnv("test")
	if !ok {
		t.Fatal("credentialEnv() ok = false, want true: a missing GitHub token must not fail the command")
	}
	if _, present := env["GH_TOKEN"]; present {
		t.Errorf("credentialEnv()[GH_TOKEN] = %q, want absent when no GitHub token is available", env["GH_TOKEN"])
	}
}

func TestGitIdentityEnvReadsHostConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed on this machine")
	}
	isolateGitHome(t)
	if err := exec.Command("git", "config", "--global", "user.name", "Alice Example").Run(); err != nil {
		t.Fatalf("seed user.name: %v", err)
	}
	if err := exec.Command("git", "config", "--global", "user.email", "alice@example.com").Run(); err != nil {
		t.Fatalf("seed user.email: %v", err)
	}

	got := gitIdentityEnv()
	if got["CLAUDIO_GIT_NAME"] != "Alice Example" {
		t.Errorf("gitIdentityEnv()[CLAUDIO_GIT_NAME] = %q, want %q", got["CLAUDIO_GIT_NAME"], "Alice Example")
	}
	if got["CLAUDIO_GIT_EMAIL"] != "alice@example.com" {
		t.Errorf("gitIdentityEnv()[CLAUDIO_GIT_EMAIL] = %q, want %q", got["CLAUDIO_GIT_EMAIL"], "alice@example.com")
	}
}

func TestGitIdentityEnvNilWhenHostHasNone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed on this machine")
	}
	isolateGitHome(t) // no user.name / user.email set

	got := gitIdentityEnv()
	if got != nil {
		t.Errorf("gitIdentityEnv() = %v, want nil when host has no configured identity", got)
	}
}

// A host with only one of the two set shouldn't fail or inject bogus
// defaults — pipe through what's there, leave the other unset so the
// entrypoint's "write when absent" check sees the gap and skips.
func TestGitIdentityEnvPartial(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed on this machine")
	}
	isolateGitHome(t)
	if err := exec.Command("git", "config", "--global", "user.email", "only@example.com").Run(); err != nil {
		t.Fatalf("seed user.email: %v", err)
	}

	got := gitIdentityEnv()
	if got["CLAUDIO_GIT_EMAIL"] != "only@example.com" {
		t.Errorf("gitIdentityEnv()[CLAUDIO_GIT_EMAIL] = %q, want %q", got["CLAUDIO_GIT_EMAIL"], "only@example.com")
	}
	if _, present := got["CLAUDIO_GIT_NAME"]; present {
		t.Errorf("gitIdentityEnv()[CLAUDIO_GIT_NAME] = %q, want absent when user.name is unset", got["CLAUDIO_GIT_NAME"])
	}
}

func TestGitIdentityEnvNilWhenNoGit(t *testing.T) {
	t.Setenv("PATH", "") // git unreachable

	got := gitIdentityEnv()
	if got != nil {
		t.Errorf("gitIdentityEnv() = %v, want nil when `git` isn't on PATH", got)
	}
}

func TestCredentialEnvMergesGitIdentity(t *testing.T) {
	gitDir := pathForGit(t) // skips if git isn't installed
	clearGitHubEnv(t)       // also clears PATH, which would prevent git lookup — restore after
	t.Setenv("PATH", gitDir)
	isolateGitHome(t)
	if err := exec.Command("git", "config", "--global", "user.name", "Alice").Run(); err != nil {
		t.Fatalf("seed user.name: %v", err)
	}
	if err := exec.Command("git", "config", "--global", "user.email", "alice@example.com").Run(); err != nil {
		t.Fatalf("seed user.email: %v", err)
	}
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "anthropic-token")

	env, ok := credentialEnv("test")
	if !ok {
		t.Fatal("credentialEnv() ok = false, want true with an Anthropic token set")
	}
	if env["CLAUDIO_GIT_NAME"] != "Alice" {
		t.Errorf("credentialEnv()[CLAUDIO_GIT_NAME] = %q, want %q", env["CLAUDIO_GIT_NAME"], "Alice")
	}
	if env["CLAUDIO_GIT_EMAIL"] != "alice@example.com" {
		t.Errorf("credentialEnv()[CLAUDIO_GIT_EMAIL] = %q, want %q", env["CLAUDIO_GIT_EMAIL"], "alice@example.com")
	}
}

func TestCredentialEnvOKWithNoGitIdentity(t *testing.T) {
	gitDir := pathForGit(t)
	clearGitHubEnv(t)
	t.Setenv("PATH", gitDir)
	isolateGitHome(t) // no identity configured
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "anthropic-token")

	env, ok := credentialEnv("test")
	if !ok {
		t.Fatal("credentialEnv() ok = false, want true: a missing git identity must not fail the command")
	}
	if _, present := env["CLAUDIO_GIT_NAME"]; present {
		t.Errorf("credentialEnv()[CLAUDIO_GIT_NAME] = %q, want absent", env["CLAUDIO_GIT_NAME"])
	}
	if _, present := env["CLAUDIO_GIT_EMAIL"]; present {
		t.Errorf("credentialEnv()[CLAUDIO_GIT_EMAIL] = %q, want absent", env["CLAUDIO_GIT_EMAIL"])
	}
}

// pathForGit returns a minimal PATH that only contains the directory
// holding `git`, so a test can clear everything else (via clearGitHubEnv)
// without also losing `git` itself.
func pathForGit(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed on this machine")
	}
	// p is .../bin/git; return .../bin
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}
