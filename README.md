# claudio

claudio runs several [Claude Code](https://claude.ai/code) sessions in parallel, each in its own sandboxed container with a real git checkout and reachable ports. Instead of one Claude Code session tying up your one working tree, each session gets its own worktree, its own container, and its own ports — so you can run five lines of work at once without them stepping on each other.

## Requirements

- A Docker-API-compatible container runtime. **[OrbStack](https://orbstack.dev/) is recommended** on macOS; Docker Desktop and native Linux Docker are also supported.
- A Claude subscription, and the `claude` CLI installed (`claude setup-token` — see [Getting started](#getting-started) below).
- SSH access to the repos you want to work in (claudio clones over `git@host:path`, `ssh://`, or `https://` — see [Cloning](#cloning)).
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
export CLAUDE_CODE_OAUTH_TOKEN=$(claude setup-token)   # one-time; see below
claudio create git@github.com:acme/web.git
claudio ls
claudio attach brave-otter   # use the ID claudio printed
```

`create` clones the repo (once — see [Repo layout](#repo-layout)), adds a worktree, builds or reuses an image, and starts a container. `attach` drops you into the real Claude Code TUI inside it, with full color and resize support. Detach with `Ctrl-b d`; the session keeps running. `claudio ls --all` shows stopped instances too.

## Getting started

### First-run auth

Claudio needs an Anthropic credential to give each container. Run:

```bash
claude setup-token
```

This is a one-time interactive step that converts your existing Claude subscription into a long-lived token, so instances bill against your subscription rather than metered API usage. Export the result as `CLAUDE_CODE_OAUTH_TOKEN` (or set `ANTHROPIC_API_KEY` instead, if you'd rather use metered billing) before running `claudio create`/`start`/`restart` — claudio reads it from your shell environment and injects it into the container it starts.

**Current limitation**: claudio does not yet store this credential anywhere (a host keychain, `~/.claudio/auth/`) — you need it exported in whatever shell you run `claudio create` from, every time. It's also the *same* credential in every container: an agent that reads its own environment holds your subscription token, and revoking it stops every running instance at once. A credential broker that fixes both of these is planned but not built yet.

### Where the files are

```bash
claudio cd brave-otter
```

prints the instance's workspace path — a plain directory on your host, openable in any editor, not something living only inside the container. Wrap it in a shell function for `cd`-in-place:

```bash
cdc() { cd "$(claudio cd "$1")"; }
```

Workspaces live under `~/.claudio/repos/<repo-slug>/worktrees/<instance-id>/` (see [Repo layout](#repo-layout) for why repos and worktrees are split). The container mounts the whole repo root, not just the worktree — that's what makes the worktree's `.git` file resolve correctly inside the container.

### Ports

An instance's app doesn't get port 3000 — it gets whatever's free in `43000–43999` (configurable, see [`~/.claudio/config.yml`](#claudioyml-and-configyml)) on `127.0.0.1`. This is what lets N instances of the same repo run at once without their dev servers colliding. See the mapping with:

```bash
claudio status brave-otter   # or: claudio ls, which shows it in a column
```

If a port claudio guessed is wrong (or a service it didn't detect needs one), correct it with `claudio ports <id> --add <container-port>` — see [`.claudio.yml`](#claudioyml-and-configyml) below for declaring one permanently instead.

### Working with several instances

This is the actual point of the tool. `claudio ls` lists everything running (add `--all` for stopped instances too); `claudio ls --json` if you're scripting against it. Each instance is independent: its own branch, its own container, its own ports, so you can have one running a long build, another mid-review, another exploring a fix, without any of them touching each other's files.

## Cloning

`claudio create <repo>` accepts `git@host:path`, `ssh://[user@]host/path`, or `https://host/path` — **not** a bare `owner/repo` shorthand. The repo is cloned once per remote URL and reused; each `create` against the same URL adds a new worktree rather than re-cloning.

For work with no upstream repo at all (a scratch analysis, a greenfield prototype), use:

```bash
claudio create --new my-experiment
```

which `git init`s a fresh root instead of cloning anything.

## Branches

By default, `create` makes a new branch named `claudio/<instance-id>` off the repo's default branch. Override it:

- `--branch <name>` checks out an *existing* branch — it must already exist in the repo.
- `--new-branch <name>` creates a new branch with the name you choose, instead of the generated default.

**Two instances cannot share a branch.** This isn't an arbitrary rule — it falls directly out of git worktrees, which refuse to check out the same branch twice. If you try, `create` reports the collision and offers a suggested alternative name (or `--yes` to accept it non-interactively) rather than failing with git's own raw error.

## Resource limits

Every instance gets a memory/CPU/PID ceiling so a runaway build in one container can't take down the machine (or your other instances). Defaults are conservative — 6 GB memory, 4 CPUs, 512 PIDs — sized against your container runtime's actual memory budget (which is smaller than your host's total RAM if you're on OrbStack or Docker Desktop's own VM), not the host's full capacity. Override per-repo in `.claudio.yml`, or per-instance:

```bash
claudio create git@github.com:acme/web.git --memory 10g --cpus 6 --pids 1024
```

A local override always wins over what a repo's `.claudio.yml` asks for — `create` tells you when it's doing that, rather than silently picking a different number than what's committed. If an instance gets killed for exceeding its memory limit, `claudio ls`/`claudio status` say so explicitly (colored, and distinct from a plain stop) instead of leaving you to guess why it stopped.

## Adding a database or another service

Don't install Postgres into the agent's own image. If your repo ships a `docker-compose.yml` (or `compose.yaml`), claudio detects it automatically: `create` starts every service in it as its own sidecar container, on a per-instance network, with the agent joined to that same network as an extra service. The agent reaches `db` (or whatever the service is named) by that name, exactly as the repo already expects — claudio rewrites every published port to one it allocates itself, so two instances of the same repo never collide over host port 5432.

If you don't want to maintain a full compose file just for a sidecar or two, declare them directly in `.claudio.yml`:

```yaml
services:
  - name: db
    image: postgres:16
    env:
      POSTGRES_PASSWORD: dev
  - name: cache
    image: redis:7
```

These are internal-only (reached by service name, never published to the host) — if you need a sidecar's port reachable from your host too, use a real compose file.

`claudio stop`/`start`/`restart`/`destroy` all treat a compose-backed instance as a whole project: `stop` brings down every container (sidecar data survives, same as a single-container instance's workspace); `destroy` removes everything including sidecar volumes. `claudio status` shows each sidecar's live state; `claudio logs <id> --service db` reaches one sidecar's logs specifically (omit `--service` to interleave every service's logs).

**Client tools, not the service itself, go in the agent image** — if your app needs `psql` or `redis-cli` to talk to a sidecar, declare that in `.claudio.yml`'s `image:` section (see below), not as a service.

## `.claudio.yml` and `config.yml`

Two files, two audiences. `<repo>/.claudio.yml` is **what this project needs** — commit it, share it. `~/.claudio/config.yml` is **what this machine allows** — personal, uncommitted, applies to every instance regardless of repo.

`.claudio.yml` (all fields optional):

```yaml
image:
  base: node:22-slim       # default; override for a different toolchain base
  apt: [libpq-dev]         # extra apt packages baked into a per-repo image layer
  npm_global: [pnpm]
  dockerfile: .claudio/Dockerfile   # escape hatch: replaces the generated Dockerfile entirely

ports:
  - name: web
    container: 3000
    expose: false           # container-internal only; omit or true to publish to the host

services:                   # sidecars synthesized into a compose project — see above
  - name: db
    image: postgres:16
    env:
      POSTGRES_PASSWORD: dev

post_create:                 # run once, inside the container, after the workspace is mounted
  - npm ci

resources:
  memory: 10g               # this repo needs more than the machine default
  cpus: 6
  pids: 1024
```

A repo declaring an `image:` section with `apt`/`npm_global`/`dockerfile` needs its own image layer, built on top of the shared base — build it explicitly:

```bash
claudio image build --repo /path/to/your/clone
```

`create` never builds an image as a side effect (a multi-minute, network-dependent build has no business happening inside what's supposed to be a fast provisioning step) — if the image it needs doesn't exist yet, it tells you the exact `claudio image build --repo ...` command to run.

`~/.claudio/config.yml`:

```yaml
workspace_root: ~/.claudio     # where repos/worktrees/state live
resources:
  memory: 6g
  cpus: 4
  pids: 512
ports:
  range: [43000, 43999]
  bind: 127.0.0.1               # never 0.0.0.0 by default — see --publish-all-interfaces
runtime:
  docker_host: ""                # pin a specific Docker endpoint; empty auto-detects
```

## Untracked containers

If claudio's state database is lost, rebuilt from a backup, or a container is created/removed outside claudio entirely, `claudio ls` shows it separately as **untracked** rather than silently ignoring it. Two ways to resolve one:

- `claudio adopt <container>` reconstructs a store row from the container's own labels, so claudio starts managing it again.
- `claudio forget <container>` removes it permanently, with no attempt to adopt it.

## Non-obvious decisions worth knowing

- **`docker info`, not `docker --version`**, is how claudio detects your runtime. `docker --version` only reports the CLI client version — on a machine with both Docker Desktop and OrbStack installed, the *client* can report one runtime while `docker info` (and every actual `docker` command) talks to a completely different one. This project got misled by that once; `docker info` is the only check that's actually reliable.
- **No Docker socket is mounted into agent containers, on purpose.** Mounting `/var/run/docker.sock` is a direct path to host-root access from inside the sandbox. If a repo genuinely needs to run `docker`/`docker compose` itself, that needs a separate, explicitly opt-in rootless Docker-in-Docker sidecar — not something claudio does by default.
- **Dependency directories (`node_modules`, etc.) are bind-mounted, not offloaded to a named volume.** Volume offloading was measured and rejected: it's 7–15× faster on filesystem metadata operations, but the host loses proper visibility into the directory — which defeats the entire reason to use claudio instead of a headless remote sandbox. If your build is metadata-heavy and slow on a bind mount, that trade was made deliberately; see [`docs/architecture.md`'s Appendix A](docs/architecture.md#appendix-a-mount-strategy-evidence) for the actual measurements rather than re-litigating it from scratch.
- **The Anthropic credential is shared across every container** (see [First-run auth](#first-run-auth) above) — there is no per-instance isolation yet. Don't assume revoking one instance's access is possible without revoking all of them.

## Troubleshooting

**"every host port in N-M is taken"** — the configured port range is exhausted. Either widen `ports.range` in `~/.claudio/config.yml`, or free some up: `claudio ls` to see what's running, `claudio destroy` anything you don't need.

**"the container runtime is unreachable"** — Docker (or OrbStack) isn't running, or claudio can't reach it. Run `docker info` yourself first; if that works but claudio still can't connect, check `runtime.docker_host` in your config.

**Container exited immediately / "stopped (out of memory)"** — `claudio ls`/`claudio status` will say if it was an OOM kill specifically. Raise the limit with `--memory` on `create`, or in `.claudio.yml`'s `resources:`.

**Clone failed over SSH** — claudio clones exactly the way `git clone` would from your shell; if `git clone <repo>` doesn't work standalone, `claudio create` won't either. Check your SSH agent has the right key loaded.

**Sidecar unhealthy** — `claudio status <id>` lists each sidecar's live state; `claudio logs <id> --service <name>` gets its actual output.

**Files in the workspace are owned by the wrong user / need `sudo` to touch** — this shouldn't happen: the base image is built with `USER_UID`/`USER_GID` matched to your host user specifically to avoid it. If it does, `docker image inspect claudio/base:latest` and confirm the UID matches `id -u` on your host — a stale image built before a UID change is the most likely cause; `claudio image build` again.

**"no such instance"** — the ID or name doesn't match anything claudio knows about. `claudio ls --all` to see everything, including stopped instances (a destroyed instance is gone for good, not just hidden).

---

See [`docs/architecture.md`](docs/architecture.md) for how claudio is built, if you're contributing rather than just using it.
