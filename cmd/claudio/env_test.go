// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"os/exec"
	"testing"
)

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
