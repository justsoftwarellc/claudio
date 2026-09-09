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
tmux new-session -d -s "$SESSION" -c "$PWD"

# Keep the session alive when its pane's process exits (ROD-116). The
# pane's shell is the session's only process, and tmux tears down the
# session — and with it the entire server, since this is the only
# session — the moment that shell exits. That made an ordinary Ctrl-C
# sequence destructive: Claude Code quits on double Ctrl-C, leaving a
# bare shell, and one more Ctrl-C/Ctrl-D exits that shell, taking the
# session with it. The container stays Up regardless (the `tail -f`
# below is what holds it open, not tmux), so `claudio ls` kept reporting
# the instance healthy while `claudio attach` had nothing left to attach
# to. Verified empirically: without this, `tmux ls` reports "no server
# running" once the pane's shell exits.
#
# remain-on-exit leaves the pane in a dead state instead of destroying
# it; `claudio attach` respawns a dead pane on its way in, so the user
# gets a live shell back rather than a frozen one.
tmux set-option -t "$SESSION" remain-on-exit on

# Launch Claude Code inside the session rather than as this script's own
# exec target — see §7.2 for why (detach semantics, multi-viewer,
# scrollback for the future activity monitor in ROD-102).
tmux send-keys -t "$SESSION" 'claude' C-m

# Keep this process (tini's child) alive for as long as the tmux server
# is running, so `docker stop` has something to signal and the container
# doesn't exit the moment the session is created.
exec tail -f /dev/null
