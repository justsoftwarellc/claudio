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
// Also folds in a GitHub token (see githubEnv) under the same
// passthrough model. Unlike the Anthropic credential, its absence does
// not fail the command — an instance with no GitHub token still works
// for everything that doesn't fetch/push, so this only gates on the
// credential the container cannot run Claude Code at all without.
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
