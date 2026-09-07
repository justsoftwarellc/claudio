#!/bin/bash
# Claudio container entrypoint — the supervisor from docs/architecture.md
# §7.2. Runs as PID 1's child under tini. Starts a tmux server hosting a
# session named "claude", with Claude Code running inside it — not as
# PID 1 directly, so the user can detach without signalling the process,
# multiple viewers can attach, and the session survives a client
# disconnect (§7.2).
set -euo pipefail

SESSION=claude

if [ -z "${CLAUDE_CODE_OAUTH_TOKEN:-}" ] && [ -z "${ANTHROPIC_API_KEY:-}" ]; then
	echo "entrypoint: no CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY set." >&2
	echo "entrypoint: the container has no credential to run Claude Code. See ROD-96." >&2
	exit 1
fi

# Start a detached tmux session so it exists before anyone attaches, and
# survives this script's own exit (tini keeps the tmux server process
# alive as long as it has a client or a session — the container's
# lifetime is the tmux server's lifetime here).
tmux new-session -d -s "$SESSION" -c /workspace

# Launch Claude Code inside the session rather than as this script's own
# exec target — see §7.2 for why (detach semantics, multi-viewer,
# scrollback for the future activity monitor in ROD-102).
tmux send-keys -t "$SESSION" 'claude' C-m

# Keep this process (tini's child) alive for as long as the tmux server
# is running, so `docker stop` has something to signal and the container
# doesn't exit the moment the session is created.
exec tail -f /dev/null
