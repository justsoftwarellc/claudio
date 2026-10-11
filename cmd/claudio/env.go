// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// credentialEnv reads the Anthropic credential (CLAUDE_CODE_OAUTH_TOKEN
// or ANTHROPIC_API_KEY, docs/architecture.md §8.1) from this process's
// own environment — never logged, never written anywhere but the new
// container's env. Shared by every command that provisions a container
// (create, start, restart): ROD-108's credential broker will replace
// this direct passthrough with something that also handles rotation;
// until then, the operator's shell is the credential's only source.
//
// Also folds in a GitHub token (see githubEnv) and the operator's
// configured git identity (see gitIdentityEnv) under the same
// passthrough model. Unlike the Anthropic credential, their absence
// does not fail the command — an instance with no GitHub token still
// works for everything that doesn't fetch/push, and an instance with
// no git identity still works for everything that isn't a commit — so
// this only gates on the credential the container cannot run Claude
// Code at all without.
//
// caller names the command in the printed diagnostic (e.g. "claudio
// create") so a failure points at the actual command the user ran.
func credentialEnv(caller string) (map[string]string, bool) {
	env := map[string]string{}
	if v := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); v != "" {
		env["CLAUDE_CODE_OAUTH_TOKEN"] = v
	}
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		env["ANTHROPIC_API_KEY"] = v
	}
	if env["CLAUDE_CODE_OAUTH_TOKEN"] == "" && env["ANTHROPIC_API_KEY"] == "" {
		fmt.Fprintf(os.Stderr, "%s: no CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY in this shell's environment.\n", caller)
		fmt.Fprintf(os.Stderr, "%s: run `claude setup-token` and export the result first (see docs/architecture.md §8.1).\n", caller)
		return nil, false
	}
	for k, v := range githubEnv() {
		env[k] = v
	}
	for k, v := range gitIdentityEnv() {
		env[k] = v
	}
	return env, true
}

// githubEnv reads a GitHub token from this process's own environment —
// GH_TOKEN then GITHUB_TOKEN, the same two names (and precedence) `gh`
// itself honours — falling back to `gh auth token` when neither is set
// and `gh` is on PATH and logged in. image/entrypoint.sh turns whichever
// one comes back into a git credential helper, so `git fetch`/`git push`
// and `gh` work inside the container against the operator's own GitHub
// access (ROD-108 found during attach testing: the container previously
// had no git credentials at all, only the Anthropic one above).
//
// This is the same shared-secret model as the Anthropic token, and the
// same caveat applies: every container gets this one token, and
// revoking it ends GitHub access for every running instance at once.
// ROD-108's credential broker (daemon-mediated, per-instance, gateable)
// is the real fix; this is the stopgap until that's built.
func githubEnv() map[string]string {
	if v := os.Getenv("GH_TOKEN"); v != "" {
		return map[string]string{"GH_TOKEN": v}
	}
	if v := os.Getenv("GITHUB_TOKEN"); v != "" {
		return map[string]string{"GH_TOKEN": v}
	}
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return nil
	}
	out, err := exec.Command(ghPath, "auth", "token").Output()
	if err != nil {
		return nil
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return nil
	}
	return map[string]string{"GH_TOKEN": token}
}

// gitIdentityEnv reads the operator's own `git config --global
// user.name` and `user.email` and ships them to the container as
// CLAUDIO_GIT_NAME / CLAUDIO_GIT_EMAIL, which image/entrypoint.sh
// writes into the agent's ~/.gitconfig on boot. Without this, a
// commit inside the container has no author identity at all — the
// image carries no .gitconfig and the GH_TOKEN stopgap above only
// authenticates the push, not who the commit is from, so an agent's
// first `git commit` fails with "Please tell me who you are".
//
// Namespaced CLAUDIO_* rather than passed as GIT_AUTHOR_* /
// GIT_COMMITTER_* on purpose: the latter would override whatever the
// user eventually sets inside the container (home/ is persistent, so
// `git config --global` edits survive restarts and should win), while
// the entrypoint only seeds ~/.gitconfig when it has no user.email
// set — the same "write when absent" pattern CLAUDE.md and
// settings.json use elsewhere in entrypoint.sh.
//
// Soft like githubEnv: a host with no configured identity (unusual
// but valid — a fresh dev machine) just means the container ships
// without one. The agent can `git config --global` it inside, or set
// the two env vars before `claudio create`, or add them to the host's
// git config and recreate the instance.
func gitIdentityEnv() map[string]string {
	env := map[string]string{}
	if v := gitGlobalConfig("user.name"); v != "" {
		env["CLAUDIO_GIT_NAME"] = v
	}
	if v := gitGlobalConfig("user.email"); v != "" {
		env["CLAUDIO_GIT_EMAIL"] = v
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

// gitGlobalConfig returns `git config --global --get <key>`, trimmed,
// or "" when git isn't on PATH, the key isn't set, or git exits
// nonzero for any other reason. A missing identity is not an error
// here (see gitIdentityEnv's comment).
func gitGlobalConfig(key string) string {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	out, err := exec.Command(gitPath, "config", "--global", "--get", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
