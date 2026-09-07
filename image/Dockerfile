# Claudio base image. See docs/architecture.md §7.1 and ROD-96.
#
# node:22-slim rather than bare Debian: Claude Code itself needs Node
# regardless of what the repo needs, so starting from the official Node
# image avoids installing Node onto a base that doesn't have it.
FROM node:22-slim

ARG USER_UID=1000
ARG USER_GID=1000

# tini as PID 1 (zombie reaping + signal forwarding for free); tmux for
# the session Claude Code runs inside (§7.2 — detach without killing it);
# git for the worktree the container mounts; ripgrep because Claude Code
# itself uses it. curl/ca-certificates for anything the agent installs.
RUN apt-get update && apt-get install -y --no-install-recommends \
        tini \
        tmux \
        git \
        ripgrep \
        curl \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# UID/GID matched to the host at build time (build-arg, not baked) — see
# ROD-96: since /workspace is a bind mount, every file the agent creates
# carries the container's UID, and a mismatch means root-owned files in
# the user's own repo. `node:22-slim` already has uid/gid 1000 reserved
# as `node`; only create the `agent` user under a different UID/GID.
RUN if getent passwd "${USER_UID}" >/dev/null; then userdel -f "$(getent passwd "${USER_UID}" | cut -d: -f1)"; fi \
    && if getent group "${USER_GID}" >/dev/null; then groupdel "$(getent group "${USER_GID}" | cut -d: -f1)" || true; fi \
    && groupadd -g "${USER_GID}" agent \
    && useradd -m -u "${USER_UID}" -g "${USER_GID}" -s /bin/bash agent

# Claude Code CLI. Installed as the agent user's npm global so it is not
# root-owned and does not need root to update.
USER agent
ENV NPM_CONFIG_PREFIX=/home/agent/.npm-global
ENV PATH=/home/agent/.npm-global/bin:$PATH
RUN npm install -g @anthropic-ai/claude-code

# Docker's ENV PATH only reaches processes Docker launches directly
# (the entrypoint). A tmux pane's shell is an interactive/login shell
# that re-reads its own startup files and resets PATH to the system
# default — verified: `claude` was on PATH via `docker exec` but
# "command not found" inside the tmux pane. /etc/profile.d runs for
# every login shell regardless of which user's dotfiles exist, so this
# is the one place that reliably reaches the tmux pane's shell too.
USER root
RUN echo 'export PATH=/home/agent/.npm-global/bin:$PATH' > /etc/profile.d/claudio-npm-global.sh
USER agent

# safe.directory belt-and-braces (ROD-97): even with UID matching, a
# worktree mounted from the host can trip git's dubious-ownership check
# in edge cases (e.g. differing mount options). "*" is acceptable here
# because the container's only mounted content is Claudio-managed.
RUN git config --global --add safe.directory '*'

# Pre-seed onboarding state to skip the interactive first-run theme
# picker, which otherwise blocks at a stdin prompt with nothing to
# answer it. Verified empirically with a real CLAUDE_CODE_OAUTH_TOKEN:
# the prompt fires regardless of a valid credential, and is NOT gated by
# ~/.claude/settings.json's "theme" key (that alone did not skip it).
# The actual gate is ~/.claude.json's "hasCompletedOnboarding" and
# "theme" fields, confirmed by inspecting a real local install where
# onboarding was already completed. A second, distinct first-run gate
# — the "is this a project you trust?" dialog — was found immediately
# after fixing the theme prompt, using a REAL CLAUDE_CODE_OAUTH_TOKEN
# (referenced by the operator's shell, never exposed in this session).
# It is gated per-project by projects["<path>"].hasTrustDialogAccepted
# in the same file, keyed by the exact cwd Claude Code was started in.
# Since every Claudio container mounts the workspace at the fixed path
# /workspace (docs/architecture.md §5.1), that one path can be
# pre-trusted at build time — this is not a security regression for
# Claudio specifically, because the operator (not an untrusted third
# party) is the one who chose to mount that repo into a sandbox they
# provisioned in the first place.
RUN cat > /home/agent/.claude.json <<'JSON'
{
  "theme": "dark",
  "hasCompletedOnboarding": true,
  "projects": {
    "/workspace": {
      "hasTrustDialogAccepted": true
    }
  }
}
JSON

COPY --chown=agent:agent entrypoint.sh /home/agent/entrypoint.sh
RUN chmod +x /home/agent/entrypoint.sh

WORKDIR /workspace

ENTRYPOINT ["tini", "--", "/home/agent/entrypoint.sh"]
