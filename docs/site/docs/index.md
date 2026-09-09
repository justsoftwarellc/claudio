# claudio

claudio runs several [Claude Code](https://claude.ai/code) sessions in parallel, each in its own sandboxed container with a real git checkout and reachable ports. Instead of one Claude Code session tying up your one working tree, each session gets its own worktree, its own container, and its own ports — so you can run five lines of work at once without them stepping on each other.

## Requirements

- A Docker-API-compatible container runtime. **[OrbStack](https://orbstack.dev/) is recommended** on macOS; Docker Desktop and native Linux Docker are also supported.
- A Claude subscription, and the `claude` CLI installed (`claude setup-token` — see [Getting Started](./guide/getting-started.md)).
- SSH access to the repos you want to work in (claudio clones over `git@host:path`, `ssh://`, or `https://` — see [Cloning](./guide/getting-started.md#cloning)).
- Go 1.25+ to build claudio itself (no prebuilt binary yet).

## Install

```bash
git clone <this-repo>
cd claudio
go build -o /usr/local/bin/claudio ./cmd/claudio
```

Then build the base image every instance runs on:

```bash
claudio image build
```

This builds `claudio/base:latest` (Node, git, tmux, Claude Code, matched to your host UID/GID) once. Re-run it after pulling changes to `image/`; Docker's layer cache makes a no-op rebuild nearly free.

## The 60-second path

```bash
export CLAUDE_CODE_OAUTH_TOKEN=$(claude setup-token)   # one-time; see Getting Started
claudio create git@github.com:acme/web.git
claudio ls
claudio attach brave-otter   # use the ID claudio printed
```

`create` clones the repo (once), adds a worktree, builds or reuses an image, and starts a container. `attach` drops you into the real Claude Code TUI inside it, with full color and resize support. Detach with `Ctrl-b d`; the session keeps running. `claudio ls --all` shows stopped instances too.

Continue to [Getting Started](./guide/getting-started.md) for auth setup, workspace layout, and ports, or jump straight to the [Configuration reference](./guide/configuration.md) for `.claudio.yml`/`config.yml`.
