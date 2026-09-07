package main

import (
	"fmt"
	"os"
)

// credentialEnv reads the Anthropic credential (CLAUDE_CODE_OAUTH_TOKEN
// or ANTHROPIC_API_KEY, docs/architecture.md §8.1) from this process's
// own environment — never logged, never written anywhere but the new
// container's env. Shared by every command that provisions a container
// (create, start, restart): ROD-108's credential broker will replace
// this direct passthrough with something that also handles rotation;
// until then, the operator's shell is the credential's only source.
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
	return env, true
}
