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

# Claude Code CLI. Installed under /opt rather than /home/agent — ROD-99
# needs home/ bind-mounted to /home/agent for transcripts to survive a
# container rebuild (docs/architecture.md §5.1), and a bind mount at
# /home/agent replaces the directory's entire prior content. Verified
# empirically: pointing NPM_CONFIG_PREFIX at /home/agent/.npm-global (the
# original layout) meant any mount at /home/agent — even an empty host
# directory — shadowed the install completely, and `claude` disappeared
# from the container with "no such file or directory". /opt/claude-code
# sits outside every mount this image declares, so it survives regardless
# of what home/ contains. Root owns the install (root-only write is
# correct for a shared toolchain path); the agent user only ever reads it.
ENV NPM_CONFIG_PREFIX=/opt/claude-code
ENV PATH=/opt/claude-code/bin:$PATH
RUN npm install -g @anthropic-ai/claude-code \
    && chmod -R a+rX /opt/claude-code

# Docker's ENV PATH only reaches processes Docker launches directly
# (the entrypoint). A tmux pane's shell is an interactive/login shell
# that re-reads its own startup files and resets PATH to the system
# default — verified: `claude` was on PATH via `docker exec` but
# "command not found" inside the tmux pane. /etc/profile.d runs for
# every login shell regardless of which user's dotfiles exist, so this
# is the one place that reliably reaches the tmux pane's shell too.
RUN echo 'export PATH=/opt/claude-code/bin:$PATH' > /etc/profile.d/claudio-npm-global.sh

# Onboarding pre-seed template — see entrypoint.sh for why this can no
# longer be written directly to /home/agent/.claude.json at build time.
# Once home/ is bind-mounted (ROD-99), anything baked into /home/agent in
# this image is shadowed by whatever the host mount contains (verified:
# same failure mode as the npm-global path above). The entrypoint copies
# this template into place on first boot instead, i.e. only when the
# mounted home/ doesn't already have its own .claude.json — a rebuilt
# container reusing an existing home/ must never clobber a real session's
# onboarding/trust state with the template again.
#
# __CLAUDIO_WORKDIR__ is substituted by entrypoint.sh with the container's
# actual $PWD at boot — found empirically while implementing ROD-114:
# ROD-97's model mounts the whole repo root at /repo with the worktree as
# the working directory, so the trusted path is /repo/worktrees/<id>,
# different per instance, and can no longer be the fixed /workspace this
# file used to hardcode.
#
# Skips the interactive first-run theme picker, which otherwise blocks at
# a stdin prompt with nothing to answer it. Verified empirically with a
# real CLAUDE_CODE_OAUTH_TOKEN: the prompt fires regardless of a valid
# credential, and is NOT gated by ~/.claude/settings.json's "theme" key
# (that alone did not skip it). The actual gate is ~/.claude.json's
# "hasCompletedOnboarding" and "theme" fields, confirmed by inspecting a
# real local install where onboarding was already completed. A second,
# distinct first-run gate — the "is this a project you trust?" dialog —
# was found immediately after fixing the theme prompt, using a REAL
# CLAUDE_CODE_OAUTH_TOKEN (referenced by the operator's shell, never
# exposed in this session). It is gated per-project by
# projects["<path>"].hasTrustDialogAccepted in the same file, keyed by
# the exact cwd Claude Code was started in. Since every Claudio container
# mounts the workspace at the fixed path /workspace (docs/architecture.md
# §5.1), that one path can be pre-trusted in the template — this is not a
# security regression for Claudio specifically, because the operator (not
# an untrusted third party) is the one who chose to mount that repo into
# a sandbox they provisioned in the first place.
RUN mkdir -p /opt/claudio && cat > /opt/claudio/claude.json.template <<'JSON'
{
  "theme": "dark",
  "hasCompletedOnboarding": true,
  "projects": {
    "__CLAUDIO_WORKDIR__": {
      "hasTrustDialogAccepted": true
    }
  }
}
JSON
RUN chmod a+r /opt/claudio/claude.json.template

# entrypoint.sh lives under /opt, not /home/agent (ROD-114) — found the
# hard way: /home/agent/entrypoint.sh was the ENTRYPOINT's own target, so
# the instant home/ is bind-mounted (even an empty, freshly-created host
# directory — the normal case for a brand new instance), the script
# itself disappears along with everything else baked into /home/agent.
# tini then fails immediately with "exec ... failed: No such file or
# directory" before the container does anything at all. Same shadowing
# bug as the npm-global path fixed earlier in ROD-99, just for the
# script that bug fix's own verification hadn't happened to trip over
# (that test reused a home/ populated by an earlier run, never a truly
# empty one). Copied as root, before USER agent below, for the same
# reason as the template above: /opt/claudio is root-owned.
COPY entrypoint.sh /opt/claudio/entrypoint.sh
RUN chmod a+rx /opt/claudio/entrypoint.sh

# safe.directory belt-and-braces (ROD-97): even with UID matching, a
# worktree mounted from the host can trip git's dubious-ownership check
# in edge cases (e.g. differing mount options). "*" is acceptable here
# because the container's only mounted content is Claudio-managed. Must
# run as agent — a global gitconfig written by root would not be the
# config git reads once the container runs as agent.
USER agent
RUN git config --global --add safe.directory '*'

# No WORKDIR here (ROD-114): the real working directory is
# /repo/worktrees/<id>, set per container via `docker run -w`
# (internal/engine.CreateAndStart) since it differs by instance. A
# WORKDIR baked here would only be a default for runs that omit -w, and
# leaving it unset makes that omission fail loudly instead of silently
# landing somewhere wrong.
ENTRYPOINT ["tini", "--", "/opt/claudio/entrypoint.sh"]
