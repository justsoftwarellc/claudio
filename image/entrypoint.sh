#!/bin/bash
# Claudio container entrypoint — the supervisor from docs/architecture.md
# §7.2. Runs as PID 1's child under tini. Starts a tmux server hosting a
# session named "claude", with Claude Code running inside it — not as
# PID 1 directly, so the user can detach without signalling the process,
# multiple viewers can attach, and the session survives a client
# disconnect (§7.2).
set -euo pipefail

SESSION=claude

# Apply the onboarding pre-seed only if this home/ doesn't already have
# its own .claude.json — see Dockerfile for why this moved out of the
# image build. A fresh home/ (first `claudio create` for this instance)
# gets the template; a home/ from a rebuilt container (ROD-99 restart)
# keeps its real state, including whatever session history and settings
# already accumulated there.
#
# The trust-dialog entry is keyed by the exact cwd string, and (ROD-114)
# that cwd is /repo/worktrees/<id> — different per instance, not a fixed
# /workspace — so it cannot be baked into the template at image-build
# time. sed the real $PWD in at container start instead; the template
# ships with a placeholder for exactly this substitution.
if [ ! -f "$HOME/.claude.json" ] && [ -f /opt/claudio/claude.json.template ]; then
	sed "s#__CLAUDIO_WORKDIR__#$PWD#" /opt/claudio/claude.json.template > "$HOME/.claude.json"
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
MEMO
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
