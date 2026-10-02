#!/bin/bash
# Claudio container entrypoint — the supervisor from docs/architecture.md
# §7.2. Runs as PID 1's child under tini. Starts a tmux server hosting a
# session named "claude", with Claude Code running inside it — not as
# PID 1 directly, so the user can detach without signalling the process,
# multiple viewers can attach, and the session survives a client
# disconnect (§7.2).
set -euo pipefail

SESSION=claude

# Apply the onboarding pre-seed whenever this home/ lacks a *usable*
# .claude.json — see Dockerfile for why this moved out of the image
# build. A fresh home/ (first `claudio create` for this instance) gets
# the template; a home/ from a rebuilt container (ROD-99 restart) keeps
# its real state, including whatever session history and settings
# already accumulated there.
#
# The trust-dialog entry is keyed by the exact cwd string, and (ROD-114)
# that cwd is /repo/worktrees/<id> — different per instance, not a fixed
# /workspace — so it cannot be baked into the template at image-build
# time. sed the real $PWD in at container start instead; the template
# ships with a placeholder for exactly this substitution.
#
# The guard tests *content*, not mere presence, because presence alone
# let the attach trap come back (ROD-140). Observed on a real instance:
# ~/.claude.json was truncated to 0 bytes (the container stopped while
# Claude Code was writing it), so Claude Code backed it up as
# .claude/backups/.claude.json.corrupted.<ms> and wrote itself a fresh
# minimal config — one carrying neither hasCompletedOnboarding nor a
# trust entry for the cwd. A `[ ! -f ]` guard sees that file and skips,
# so the pre-seed was never re-applied and every launch landed in the
# first-run flow (theme picker, then login).
#
# That is what resurrects the ROD-116 symptom rather than any regression
# in the pane command: Ctrl-C at an onboarding prompt is not a session
# quit, so `claude` exits *nonzero*, the pane falls through to `bash -l`
# (the user is "left in the container"), and leaving that shell loops
# round to another onboarding prompt. The pane's `claude && break` is
# correct and still verified — a real double-Ctrl-C quit of a working
# TUI exits 0 and does end the session.
#
# So the keys that gate startup are merged in on every boot, not just
# when the file is absent, and everything else in the file is preserved
# verbatim. node is the image's own runtime (Claude Code needs it), so
# this costs no extra dependency, and it is the only way to edit JSON
# without risking the truncation that caused the bug in the first place:
# the merge writes a temp file and renames it over the original, which
# is atomic, so an interrupted boot can no longer leave a 0-byte config.
if [ -f /opt/claudio/claude.json.template ]; then
	CLAUDIO_WORKDIR="$PWD" node -e '
		const fs = require("fs");
		const path = process.env.HOME + "/.claude.json";
		const workdir = process.env.CLAUDIO_WORKDIR;

		// The template supplies the baseline; reading it here keeps the
		// one definition of "what Claude Code needs to start" in the
		// image, matching core.configBaseline on the host side.
		//
		// A template that is empty or unparseable is a packaging fault,
		// not a user state, and it is the exact shape of the second
		// ROD-140 cause (a BuildKit-only heredoc that built a 0-byte
		// file under the legacy builder). Say so rather than dying with
		// a bare SyntaxError, since the visible symptom is otherwise
		// just Claude Code opening its first-run flow.
		const raw = fs.readFileSync("/opt/claudio/claude.json.template", "utf8");
		if (raw.trim() === "") {
			console.error("entrypoint: /opt/claudio/claude.json.template is empty; the image was built wrong.");
			process.exit(1);
		}
		const tmpl = JSON.parse(raw.replaceAll("__CLAUDIO_WORKDIR__", workdir));

		// An unreadable or unparseable file is treated as absent, which
		// is how Claude Code itself treats it — that includes the
		// 0-byte case that produced this bug.
		let cfg = {};
		try { cfg = JSON.parse(fs.readFileSync(path, "utf8")) || {}; } catch {}
		if (typeof cfg !== "object" || Array.isArray(cfg)) cfg = {};

		// Merge, not overwrite: only the gating keys are asserted, so
		// accumulated state (MCP servers, history, other projects)
		// survives a restart. projects is merged per-key for the same
		// reason — replacing the map would drop every other path the
		// user has already trusted.
		const before = JSON.stringify(cfg);
		for (const [k, v] of Object.entries(tmpl)) {
			// theme is a real user preference once onboarding is done,
			// so the template only fills it in when the file has none —
			// asserting it would undo `/theme` on every restart. The
			// startup gates (hasCompletedOnboarding, the trust entry)
			// are asserted unconditionally, since a stale `false` is
			// exactly the state this repairs.
			if (k === "projects") continue;
			if (k === "theme" && cfg.theme !== undefined) continue;
			cfg[k] = v;
		}
		// Per-project merge, one level deeper than Object.assign would
		// go: the template entry for this cwd carries only
		// hasTrustDialogAccepted, so assigning it wholesale would drop
		// the accumulated keys of that project (its history, its
		// allowedTools). Verified — a worktree entry with history lost
		// it under the shallower merge.
		cfg.projects = Object.assign({}, cfg.projects);
		for (const [k, v] of Object.entries(tmpl.projects || {})) {
			cfg.projects[k] = Object.assign({}, cfg.projects[k], v);
		}

		if (JSON.stringify(cfg) === before) process.exit(0);
		// Atomic replace, so an interrupted write cannot truncate the
		// config the way the original failure did.
		const tmp = path + ".claudio-tmp";
		fs.writeFileSync(tmp, JSON.stringify(cfg, null, 2) + "\n");
		fs.renameSync(tmp, path);
	' || echo "entrypoint: could not pre-seed ~/.claude.json; Claude Code may show its first-run prompts." >&2
fi

# Tell the agent how to make a dev server reachable from the host.
#
# Published ports forward to the container's external interface, so an app
# listening on loopback *inside* the container is unreachable from the
# host even when its port is published — verified: a server on 127.0.0.1
# answers in-container and not from the host. Most dev servers default to
# loopback, so without this the port mapping looks correct and the browser
# still gets nothing.
#
# This goes in user memory (~/.claude/CLAUDE.md) rather than the repo's
# own CLAUDE.md, which belongs to the user and is often checked in.
# Written only when absent, so anything the user adds here survives a
# container rebuild (home/ is bind-mounted and persists — ROD-99).
#
# An env var would not be enough: HOST=0.0.0.0 is honoured by some
# frameworks but ignored by Vite, which only takes --host (verified
# against vite 5 in a real container). Guidance covers the general case
# where a single variable cannot.
if [ ! -f "$HOME/.claude/CLAUDE.md" ]; then
	mkdir -p "$HOME/.claude"
	cat > "$HOME/.claude/CLAUDE.md" <<'MEMO'
# Running apps in this container

This is a Claudio sandbox. Ports are published to the host, but only from
the container's external interface.

**Always bind dev servers and any other app to `0.0.0.0`, never
`localhost` or `127.0.0.1`.** An app on loopback cannot be reached from
the host even when its port is published.

    vite --host 0.0.0.0        # not just `vite`
    next dev -H 0.0.0.0
    python -m http.server --bind 0.0.0.0
    # Node: server.listen(port, '0.0.0.0')

Note that `HOST=0.0.0.0` works for some tools but is ignored by others
(Vite among them) — prefer the explicit flag.

Only ports mapped for this instance are reachable. To expose one that is
not yet mapped, the user runs `claudio ports <id> --add <port>` on the
host, then `claudio restart <id>` — which replaces the container, so the
app has to be started again afterwards.

To have an app start automatically instead — including after a restart —
add it to `post_start:` in the repo's `.claudio.yml`:

    post_create:          # once, at create: dependencies
      - npm ci
    post_start:           # every start: the server itself
      - npm start

`post_start` commands are backgrounded; their output goes to
/tmp/claudio-post-start.log inside this container. Note that
`post_create` alone is NOT enough for anything that must be *running* —
it is skipped on restart.
MEMO
fi

# .claudio.yml is the user's own local config (ROD-133): it lives in the
# folder they work in on the host, and Claudio reads and writes it there
# on their behalf. The agent editing it would silently change the
# instance's own ports, hooks and resource requests behind the user's
# back, so deny the edit tools on it.
#
# A permission rule rather than sandbox.filesystem.denyWrite, which was
# tried first: permission rules apply to every tool (Bash, Read, Edit,
# MCP), while sandbox.filesystem applies only to Bash and its children.
# The sandbox layer is unavailable here in any case — verified, bubblewrap
# cannot create a namespace inside this container ("Creating new namespace
# failed: Operation not permitted"), not as the agent user and not as
# root, because Docker's default seccomp profile blocks it.
#
# Edit( alone, with no Write( companion: Claude Code rejects a Write
# rule here at startup — "Write(//repo/**/.claudio.yml) is not matched by
# file permission checks — only Edit(path) rules are. Use
# Edit(//repo/**/.claudio.yml) instead (Edit rules cover all file-editing
# tools)" — printed on every launch, above the TUI. Edit( already covers
# Write and the other file-editing tools, so the second rule bought
# nothing and cost a warning on every attach.
#
# This is advisory-grade: it stops the agent's own tools, not a
# determined `sh -c`. The container boundary, not this file, is the real
# isolation.
#
# Written only when absent, like CLAUDE.md above, so anything the user
# adds here survives a container rebuild (home/ is bind-mounted, ROD-99).
if [ ! -f "$HOME/.claude/settings.json" ]; then
	mkdir -p "$HOME/.claude"
	cat > "$HOME/.claude/settings.json" <<'SETTINGS'
{
  "permissions": {
    "deny": [
      "Edit(//repo/**/.claudio.yml)"
    ]
  }
}
SETTINGS
fi

if [ -z "${CLAUDE_CODE_OAUTH_TOKEN:-}" ] && [ -z "${ANTHROPIC_API_KEY:-}" ]; then
	echo "entrypoint: no CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY set." >&2
	echo "entrypoint: the container has no credential to run Claude Code. See ROD-96." >&2
	exit 1
fi

# Start a detached tmux session so it exists before anyone attaches, and
# survives this script's own exit (tini keeps the tmux server process
# alive as long as it has a client or a session — the container's
# lifetime is the tmux server's lifetime here).
#
# The session's cwd is wherever the container was started with -w, not a
# hardcoded /workspace: ROD-97 mounts the whole repo root at /repo with
# the worktree as the working directory (docs/architecture.md Appendix
# B), so the actual path is /repo/worktrees/<id> and varies per instance.
# Found empirically — the image's old WORKDIR /workspace no longer exists
# at all once only /repo is mounted, and tmux new-session -c on a
# nonexistent directory fails, taking the whole entrypoint down with it
# under set -e.
# What the pane runs, and why it isn't just `claude` (ROD-116).
#
# The pane's process is the session's only process, so when it exits tmux
# destroys the session and — this being the only session — the whole tmux
# server. The container stays Up regardless (the `tail -f` below is what
# holds it open, not tmux), which is how `claudio ls` came to report a
# healthy instance that `claudio attach` could no longer reach.
#
# The rule this encodes: quitting Claude Code deliberately should end the
# session and return the user to their *host* shell, exactly as they
# expect. Anything else — a crash, a bad credential — should leave the
# session standing so there is something to attach to and debug.
#
# Claude Code distinguishes the two by exit status: quitting with a
# double Ctrl-C exits 0 (verified), while a failure to start exits
# nonzero. So a clean exit ends the pane, tmux tears the session down,
# the `docker exec` behind `claudio attach` returns, and the user is back
# on the host. A nonzero exit drops to an interactive shell instead,
# which keeps the session alive and gives them somewhere to look; leaving
# that shell retries Claude Code rather than stranding them.
#
# Rejected alternatives, both tried:
#   - `remain-on-exit on`: keeps the session but leaves a *dead* pane, so
#     a user who quits while attached is stuck on "Pane is dead" with no
#     way to type — worse than the bug it fixed.
#   - An unconditional `while true; do claude || bash -l; done`: quitting
#     relaunches Claude Code immediately, so there is no way out of the
#     session at all.
PANE_CMD='while true; do claude && break; bash -l; done'
tmux new-session -d -s "$SESSION" -c "$PWD" "$PANE_CMD"

# Keep this process (tini's child) alive for as long as the tmux server
# is running, so `docker stop` has something to signal and the container
# doesn't exit the moment the session is created.
exec tail -f /dev/null
