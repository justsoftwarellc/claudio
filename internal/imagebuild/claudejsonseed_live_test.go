// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

// Verifies the ~/.claude.json pre-seed the entrypoint applies — see
// image/entrypoint.sh. Run against a real daemon for the same reason as
// the rest of this package's live tests: what matters is that Claude
// Code actually finds a startable config in a container, not that the
// script reads correctly.
//
// The regression these cover (ROD-140) is the attach trap returning:
// when ~/.claude.json exists but carries no hasCompletedOnboarding and
// no trust entry, `claude` opens its first-run flow instead of a
// session. Ctrl-C at an onboarding prompt is not a session quit, so it
// exits nonzero, the pane's `claude && break` falls through to
// `bash -l` — the user is left at a container shell — and leaving that
// shell loops round to another onboarding prompt. The pane command
// itself is correct; the config is what broke.
package imagebuild

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"
	"time"
)

// reseed re-runs the entrypoint's seeding the way a container restart
// would. The entrypoint ends in `exec tail -f /dev/null`, so this call
// never returns on its own; it is cut off by the timeout and the file
// state afterwards is what the assertions read.
func reseed(t *testing.T, name string) {
	t.Helper()
	// The entrypoint ends in `exec tail -f /dev/null`, so it never
	// returns; the seeding it does first is all this needs. Waiting for
	// the config to become startable — rather than for the process, or
	// for a fixed sleep — is what makes this deterministic, since the
	// write is what the assertions then read.
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "exec", name, "/opt/claudio/entrypoint.sh")
	if err := cmd.Start(); err != nil {
		t.Fatalf("re-run entrypoint: %v", err)
	}
	t.Cleanup(func() { cancel(); cmd.Wait() })

	deadline := time.Now().Add(30 * time.Second)
	for {
		out, err := exec.Command("docker", "exec", name,
			"cat", "/home/agent/.claude.json").Output()
		if err == nil {
			var cfg map[string]any
			if json.Unmarshal(out, &cfg) == nil && cfg["hasCompletedOnboarding"] == true {
				return
			}
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
			t.Fatalf("re-seed never produced a startable ~/.claude.json; logs:\n%s", logs)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// writeClaudeJSON puts raw bytes at ~/.claude.json, including bytes that
// are not valid JSON — the 0-byte case is exactly what produced the bug.
func writeClaudeJSON(t *testing.T, name, content string) {
	t.Helper()
	if out, err := exec.Command("docker", "exec", name, "sh", "-c",
		"printf '%s' "+shQuote(content)+" > /home/agent/.claude.json").CombinedOutput(); err != nil {
		t.Fatalf("write .claude.json: %v: %s", err, out)
	}
}

func shQuote(s string) string {
	out := "'"
	for _, r := range s {
		if r == '\'' {
			out += `'"'"'`
			continue
		}
		out += string(r)
	}
	return out + "'"
}

// readClaudeJSON parses the seeded config, failing the test if it is not
// valid JSON — an unparseable config is the failure mode, not a detail.
//
// It waits for the file rather than reading once: startBaseContainer
// returns as soon as ~/.claude/CLAUDE.md appears, and the two seeds are
// independent steps of the entrypoint, so the config is not guaranteed
// to be on disk at that moment. Polling here rather than sleeping keeps
// the test honest about what it is waiting for.
func readClaudeJSON(t *testing.T, name string) map[string]any {
	t.Helper()
	var out []byte
	deadline := time.Now().Add(20 * time.Second)
	for {
		var err error
		out, err = exec.Command("docker", "exec", name, "cat", "/home/agent/.claude.json").Output()
		if err == nil && len(out) > 0 {
			break
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
			t.Fatalf("~/.claude.json never appeared: %v; logs:\n%s", err, logs)
		}
		time.Sleep(300 * time.Millisecond)
	}
	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("seeded .claude.json is not valid JSON: %v\n%s", err, out)
	}
	return cfg
}

// assertStartable checks the two fields that decide whether `claude`
// opens a session or its first-run flow, which is the whole point of
// the seed. workdir is the container cwd the trust entry is keyed to.
func assertStartable(t *testing.T, cfg map[string]any, workdir string) {
	t.Helper()
	if cfg["hasCompletedOnboarding"] != true {
		t.Errorf("hasCompletedOnboarding = %v, want true — `claude` opens the theme picker without it", cfg["hasCompletedOnboarding"])
	}
	projects, ok := cfg["projects"].(map[string]any)
	if !ok {
		t.Fatalf("projects = %v, want a map keyed by container cwd", cfg["projects"])
	}
	entry, ok := projects[workdir].(map[string]any)
	if !ok {
		t.Fatalf("projects[%q] missing; have %v — `claude` opens the trust dialog without it", workdir, projects)
	}
	if entry["hasTrustDialogAccepted"] != true {
		t.Errorf("projects[%q].hasTrustDialogAccepted = %v, want true", workdir, entry["hasTrustDialogAccepted"])
	}
}

// seedContainer builds the base image and starts it with the real
// entrypoint, so the seeding logic under test actually executes.
func seedContainer(t *testing.T, slug string) string {
	t.Helper()
	dockerAvailable(t)
	tag := uniqueTag("claudio-" + slug)
	if err := buildBaseAs(context.Background(), tag, nil); err != nil {
		t.Fatalf("buildBaseAs: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rmi", "-f", tag).Run() })

	name := "claudio-" + slug
	exec.Command("docker", "rm", "-f", name).Run()
	startBaseContainer(t, tag, name, "")
	return name
}

// quietContainer is seedContainer with the agent stopped, for the tests
// that assert on specific *values* in ~/.claude.json rather than only on
// the gating fields.
//
// Claude Code rewrites that file continuously while it runs — it owns
// numStartups, userID, lastVersionBase and more — so a live pane races
// every such assertion: observed as a userID and a theme that came back
// changed between the write and the read. Killing the tmux session
// leaves the container up (the entrypoint's `tail -f` holds it, not
// tmux) with nothing else touching the config, so a re-seed is the only
// writer.
func quietContainer(t *testing.T, slug string) string {
	t.Helper()
	name := seedContainer(t, slug)
	// Wait for the first seed before killing the session, so this does
	// not race the very boot it is quieting.
	readClaudeJSON(t, name)
	exec.Command("docker", "exec", name, "tmux", "kill-server").Run()
	return name
}

// A fresh home/ gets the full pre-seed, so the very first attach opens a
// session rather than the first-run flow.
func TestEntrypointSeedsStartableConfig(t *testing.T) {
	name := seedContainer(t, "cjseed-fresh")
	assertStartable(t, readClaudeJSON(t, name), "/tmp") // startBaseContainer runs with -w /tmp
}

// The regression itself: a 0-byte ~/.claude.json. Observed on a real
// instance — the container stopped while Claude Code was writing the
// file. A presence-only guard (`[ ! -f ]`) skipped the seed, so the
// config stayed unstartable and every attach landed in onboarding.
func TestEntrypointReseedsTruncatedConfig(t *testing.T) {
	name := seedContainer(t, "cjseed-truncated")
	writeClaudeJSON(t, name, "")
	reseed(t, name)
	assertStartable(t, readClaudeJSON(t, name), "/tmp")
}

// The second half of the same failure: having found the file corrupt,
// Claude Code backs it up and writes itself a *valid* minimal config
// that still carries neither gating field. That file parses, so only a
// content-aware guard re-seeds it.
func TestEntrypointReseedsClaudeCodesOwnMinimalConfig(t *testing.T) {
	name := quietContainer(t, "cjseed-minimal")
	// A key Claude Code does not own, so the assertion cannot race its
	// own bookkeeping: it rewrites userID, numStartups and friends
	// whenever it runs, which is what made an earlier version of this
	// test flaky against a real boot.
	writeClaudeJSON(t, name, `{"migrationVersion":14,"claudioProbe":"keep-me"}`)
	reseed(t, name)

	cfg := readClaudeJSON(t, name)
	assertStartable(t, cfg, "/tmp")
	// Whatever was already recorded must be carried over, not
	// discarded — the seed repairs the gates, it does not reset the
	// install.
	if cfg["claudioProbe"] != "keep-me" {
		t.Errorf("claudioProbe = %v, want it preserved through the re-seed", cfg["claudioProbe"])
	}
	if cfg["migrationVersion"] != float64(14) {
		t.Errorf("migrationVersion = %v, want 14 preserved", cfg["migrationVersion"])
	}
}

// An onboarding flag reset to false is repaired too: a stale false is
// indistinguishable, to `claude`, from never having onboarded.
func TestEntrypointRepairsOnboardingFlag(t *testing.T) {
	name := seedContainer(t, "cjseed-flag")
	writeClaudeJSON(t, name, `{"hasCompletedOnboarding":false,"theme":"light"}`)
	reseed(t, name)
	assertStartable(t, readClaudeJSON(t, name), "/tmp")
}

// The other direction, and the reason the seed merges rather than
// overwrites: home/ is bind-mounted and survives rebuilds, so real
// accumulated state must outlive every restart. A wholesale template
// write would discard all of this.
func TestEntrypointPreservesExistingConfigState(t *testing.T) {
	name := quietContainer(t, "cjseed-preserve")
	writeClaudeJSON(t, name, `{
		"theme": "light",
		"hasCompletedOnboarding": true,
		"mcpServers": {"linear": {"url": "https://example.test"}},
		"projects": {
			"/tmp": {"hasTrustDialogAccepted": true, "history": ["one", "two"]},
			"/other/path": {"hasTrustDialogAccepted": true}
		}
	}`)
	reseed(t, name)

	cfg := readClaudeJSON(t, name)
	assertStartable(t, cfg, "/tmp")

	// theme is a user preference once onboarding is done; re-asserting
	// the template's "dark" would undo `/theme` on every restart.
	if cfg["theme"] != "light" {
		t.Errorf("theme = %v, want \"light\" preserved — the seed must not undo /theme", cfg["theme"])
	}
	mcp, _ := cfg["mcpServers"].(map[string]any)
	if _, ok := mcp["linear"]; !ok {
		t.Errorf("mcpServers.linear was dropped: %v", cfg["mcpServers"])
	}

	projects, ok := cfg["projects"].(map[string]any)
	if !ok {
		t.Fatalf("projects = %v, want a map", cfg["projects"])
	}
	// Another trusted path must survive: the template knows only this
	// instance's cwd, so replacing the map would untrust everything else.
	if _, ok := projects["/other/path"]; !ok {
		t.Errorf("projects[/other/path] was dropped: %v", projects)
	}
	// And the merge must go one level deeper than the project map: the
	// template's entry carries only hasTrustDialogAccepted, so a shallow
	// assign would drop this project's own accumulated keys.
	entry, ok := projects["/tmp"].(map[string]any)
	if !ok {
		t.Fatalf("projects[/tmp] = %v, want a map", projects["/tmp"])
	}
	if entry["history"] == nil {
		t.Errorf("projects[/tmp].history was dropped by the per-project merge: %v", entry)
	}
}

// The template must survive the build path `claudio image build`
// actually uses, which is the Docker Go SDK's ImageBuild — the legacy
// builder unless asked otherwise, not BuildKit. This is the check that
// was missing when ROD-140 shipped: the template was written by a
// `RUN cat <<'JSON'` heredoc, a BuildKit-only feature, so it built as a
// 0-byte file here while `docker build` on the same machine produced a
// correct 149-byte one. An empty template means no onboarding pre-seed,
// which means every attach lands in Claude Code's first-run flow.
func TestBuiltImageCarriesANonEmptyTemplate(t *testing.T) {
	dockerAvailable(t)
	tag := uniqueTag("claudio-cjtmpl")
	if err := buildBaseAs(context.Background(), tag, nil); err != nil {
		t.Fatalf("buildBaseAs: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rmi", "-f", tag).Run() })

	out, err := exec.Command("docker", "run", "--rm", "--entrypoint", "cat", tag,
		"/opt/claudio/claude.json.template").Output()
	if err != nil {
		t.Fatalf("read the template out of the built image: %v", err)
	}

	var tmpl map[string]any
	if err := json.Unmarshal(out, &tmpl); err != nil {
		t.Fatalf("template in the built image is not valid JSON (%d bytes): %v\n%s", len(out), err, out)
	}
	if tmpl["hasCompletedOnboarding"] != true {
		t.Errorf("template is missing hasCompletedOnboarding: %v", tmpl)
	}
	if _, ok := tmpl["projects"].(map[string]any)["__CLAUDIO_WORKDIR__"]; !ok {
		t.Errorf("template has no __CLAUDIO_WORKDIR__ entry for the entrypoint to substitute: %v", tmpl)
	}
}
