# Claudio — Architecture

**Status:** Draft v1
**Date:** 2026-09-05
**Scope:** An orchestrator that runs multiple sandboxed Claude Code sessions in Docker containers, each with a checked-out repository, automatic port forwarding to the host, host-side filesystem access, and interactive terminal attach.

---

## 1. Problem statement

A developer wants to run several Claude Code agents in parallel, each working on its own copy of a repository, without those agents interfering with each other or with the host machine. Each agent needs:

- **Isolation** — a container boundary, so an agent's `rm -rf`, dependency install, or runaway process cannot touch the host or a sibling session.
- **A real repository** — cloned, on a branch, with credentials to fetch and push.
- **Running services** — if the repo is a web app, `npm run dev` inside the container must be reachable from the host browser.
- **Human interaction** — the developer must be able to drop into the Claude Code session, read what it is doing, answer its questions, and steer it.
- **Host file access** — the developer must be able to open the cloned repo in their own editor, run `git diff`, and inspect changes with native tools.

The orchestrator's job is to make creating, tracking, entering, and destroying these environments a single-command operation.

---

## 2. Design principles

1. **The host owns the source of truth.** The working tree lives on the host filesystem. Containers are disposable; the repo is not.
2. **The container is a sandbox, not a pet.** Any instance can be destroyed and rebuilt from `(repo, branch, image)` without losing work that has been committed or that lives in the bind-mounted tree.
3. **State lives in one place.** A single durable store on the host records every instance. Docker is queried to reconcile, never as the sole source of truth.
4. **Attach, don't proxy.** Interaction with Claude Code is a real PTY, not a re-implemented chat protocol. The terminal the user gets is the terminal the agent sees.
5. **Ports are discovered, not configured.** The orchestrator reads the repo to learn what it serves, and allocates host ports dynamically to avoid collisions between instances.
6. **Fail visible.** Every instance has a status, a health signal, and logs the user can reach without knowing Docker.
7. **Single-user, local-only.** One operator on one machine; no outside traffic, no shared instances, no multi-tenancy. This is a deliberate scope limit, and it removes whole categories of design: no auth layer, no access control beyond file permissions, no read-only viewers, no network exposure surface. Published ports bind to `127.0.0.1`; the control socket is a Unix socket.

---

## 3. System overview

```
┌──────────────────────────────────────────────────────────────────────┐
│ HOST (macOS / Linux)                                                 │
│                                                                      │
│  ┌────────────┐        ┌──────────────────────────────────────────┐  │
│  │ claudio    │◄──────►│ claudiod  (daemon)                       │  │
│  │ CLI        │  gRPC/ │                                          │  │
│  └────────────┘  UDS   │  ┌────────────┐  ┌──────────────────┐    │  │
│                        │  │ Instance   │  │ Port Allocator   │    │  │
│  ┌────────────┐        │  │ Manager    │  │                  │    │  │
│  │ Web UI     │◄──────►│  └────────────┘  └──────────────────┘    │  │
│  │ (optional) │  HTTP  │  ┌────────────┐  ┌──────────────────┐    │  │
│  └────────────┘  + WS  │  │ Repo       │  │ Reconciler       │    │  │
│                        │  │ Provisioner│  │ (docker events)  │    │  │
│                        │  └────────────┘  └──────────────────┘    │  │
│                        │  ┌──────────────────────────────────┐    │  │
│                        │  │ State Store (SQLite)             │    │  │
│                        │  └──────────────────────────────────┘    │  │
│                        └───────────────────┬──────────────────────┘  │
│                                            │ Docker API (socket)     │
│  ~/.claudio/                               ▼                         │
│    state.db                 ┌──────────────────────────────────┐     │
│    repos/<repo>/            │ Docker Engine                    │     │
│      worktrees/<id> ─bind───┤                                  │     │
│    instances/<id>/home ─────┤  ┌────────────────────────────┐  │     │
│                             │  │ Container: claudio-<id>    │  │     │
│                             │  │  /workspace   (bind)       │  │     │
│                             │  │  /home/agent  (bind)       │  │     │
│                             │  │  tmux: claude session      │  │     │
│                             │  │  supervisor (PID 1)        │  │     │
│                             │  │  ports 3000,5432 ─┐        │  │     │
│                             │  └───────────────────┼────────┘  │     │
│                             └──────────────────────┼───────────┘     │
│                        host :43001, :43002 ◄───────┘                 │
└──────────────────────────────────────────────────────────────────────┘
```

### Components

| Component | Responsibility |
|---|---|
| **`claudio` CLI** | User-facing commands. Thin client over the daemon; the only exception is `attach`, which execs `docker exec` directly for a true TTY. |
| **`claudiod` daemon** | Long-lived host process. Owns instance lifecycle, state, port allocation, and Docker reconciliation. Listens on a Unix domain socket. |
| **Instance Manager** | Creates, starts, stops, and destroys instances. Drives the provisioning state machine. |
| **Repo Provisioner** | Maintains one clone per repo on the host and adds a git worktree per session; prepares the instance directory layout. |
| **Port Allocator** | Detects service ports from the repo, reserves free host ports, records the mapping. |
| **Reconciler** | Subscribes to the Docker event stream; keeps stored state in sync with reality (container died, was removed out-of-band, etc.). |
| **State Store** | SQLite on the host. Single writer (the daemon), WAL mode. |
| **Container image** | A base image with Claude Code, tmux, git, and a language toolchain; extensible per-repo. |

---

## 4. Instance model

An **instance** is the unit the orchestrator manages: one container + one workspace + one Claude Code session + a set of port mappings.

```
Instance
  id            string       # short, stable, human-typeable: "brave-otter" or "ins_7f3a"
  name          string       # user-supplied label, unique
  repo_url      string
  branch        string
  commit        string       # resolved HEAD at provision time
  repo_root     string       # host path: ~/.claudio/repos/<repo>/
  worktree_dir  string       # host path: <repo_root>/worktrees/<id> — the bind mount
  container_id  string
  image         string
  status        enum         # see lifecycle
  ports         []PortMapping
  created_at    timestamp
  last_active   timestamp
  labels        map[string]string
```

```
PortMapping
  container_port int
  host_port      int
  protocol       enum        # tcp | udp
  service_name   string      # "web", "api", "postgres" — from detection
  source         enum        # detected | declared | manual
```

### 4.1 Lifecycle

```
                  ┌─────────┐
     create ─────►│ PENDING │
                  └────┬────┘
                       │ ensure root, add worktree, allocate ports
                       ▼
                ┌──────────────┐   failure   ┌────────┐
                │ PROVISIONING ├────────────►│ FAILED │
                └──────┬───────┘             └────┬───┘
                       │ container created         │ destroy
                       ▼                           │
                  ┌─────────┐                      │
         ┌───────►│ RUNNING │                      │
         │        └────┬────┘                      │
   start │             │ stop                      │
         │             ▼                           │
         │        ┌─────────┐                      │
         └────────┤ STOPPED │                      │
                  └────┬────┘                      │
                       │ destroy                   │
                       ▼                           ▼
                  ┌───────────┐              ┌───────────┐
                  │ DESTROYING├─────────────►│ DESTROYED │
                  └───────────┘              └───────────┘
```

`STOPPED` keeps the workspace on disk and the port reservations released. `DESTROYED` removes the container and, unless `--keep-workspace`, the workspace directory.

The **PROVISIONING** state is a sub-state machine, and each step is idempotent so a crashed daemon can resume:

1. Ensure the repo root exists (clone once if new); create `~/.claudio/instances/<id>/{home,logs}`
2. Add a git worktree for this session and rewrite its gitdir pointers to relative paths (§5.1)
3. Detect ports and services (§6)
4. Reserve host ports
5. Materialize per-instance config (`.claudio/resolved.yml`)
6. Create the container with mounts and port bindings
7. Start the container; supervisor launches tmux + Claude Code
8. Health-probe; transition to RUNNING

---

## 5. Filesystem and repository strategy

### 5.1 Workspace layout

**One clone per repo; one git worktree per session.** The worktree directory is what the container mounts.

```
~/.claudio/
  state.db                       # SQLite: instances, ports, events
  config.yml                     # global defaults
  auth/token                     # subscription token (0600)
  cache/                         # shared package-manager caches
  repos/
    github.com-acme-webapp/      # ← the ROOT, one per repository
      main-clone/                #   the clone: .git and the object store
      worktrees/
        brave-otter/             # ← ONE SESSION. bind-mounted into the container
        calm-finch/
  instances/
    <instance-id>/
      home/                      # agent's $HOME: transcripts, settings, shell history
      logs/
      resolved.yml               # the effective config used to build this instance
```

Cloning happens once per repo rather than once per instance, so the second and subsequent sessions on a repo are a `git worktree add` — seconds, not minutes — and they share the object store instead of duplicating it.

It also makes "one instance = one line of work" **structural rather than conventional**: git refuses to check out the same branch in two worktrees, so parallel agents cannot collide on a branch even by mistake. That refusal surfaces as a clear error naming the instance already holding it.

The worktree is a plain host directory — openable in any editor, usable with host `git`. That is the answer to "the host needs access to the folder where the repo is cloned". The root is **configurable**, defaulting to `~/.claudio`.

**Repo source: a remote GitHub URL, or a local directory.** `claudio create` accepts `git@github.com:acme/web.git`, an HTTPS URL (normalized to SSH), or the `acme/web` shorthand. The clone runs **on the host**, using the host's existing SSH setup, which is what lets the container provision without ever holding git credentials (§8).

`claudio create .` (or any path-shaped argument: `.`, `..`, `./x`, `~/x`, or an absolute path) clones from a **local directory** instead, for local-only or not-yet-pushed work (ROD-115). A directory that is not yet a git repo is `git init`ed and its contents committed first, so unversioned work gets an instance without any setup ceremony.

This was originally ruled out on the grounds that it "invites confusion about whether uncommitted work and local-only branches come along." Tested rather than assumed, `git clone <path>` answers that unambiguously, and the answer is the one a user would want:

| | Reaches the instance? |
|---|---|
| Committed history | ✅ |
| Local-only branches | ✅ as `origin/*` remote-tracking refs |
| Uncommitted / staged work | ❌ committed history only |

So an agent is never handed a half-finished edit the user has not decided to keep, and nothing has to be pushed to GitHub first. The clone is self-contained (no `.git/objects/info/alternates`), so work inside the sandbox cannot corrupt the source repo's object store, and `origin` points back at the local directory — an agent can push a finished branch straight home. A source repo that already exists is never written to: Claudio does not commit on the user's behalf there.

The path check is deliberately conservative: `acme/web` stays a GitHub shorthand even when a directory by that name exists in the cwd, so adding this never silently changes what an existing command meant.

**Initiatives without an upstream repo get the same structure.** `claudio create --new market-research` creates a root, `git init`s it, and works off a worktree exactly as a cloned repo does. Research, analysis, and writing are not second-class: they get the same isolation, the same real history, and the same diffable output.

**Branch handling** distinguishes the two intents rather than overloading one flag:

```
claudio create acme/web --branch feat/auth        # check out existing
claudio create acme/web --new-branch feat/auth    # create from the default branch
claudio create acme/web                           # generated: claudio/<instance-id>
```

A collision is not merely an error — it names the holder and offers a way forward:

```
$ claudio create acme/web --branch feat/auth
! feat/auth is already checked out by instance brave-otter

  Create feat/auth-2 instead? [Y/n]
  Or: claudio attach brave-otter   to join the existing session
```

The suggestion is the requested name with a numeric suffix past any existing collision. `--yes` accepts it; a non-TTY invocation errors with the suggestion in the message rather than hanging.

#### Worktrees across the container boundary

A worktree's `.git` is a **file**, not a directory: it contains a path to `<root>/main-clone/.git/worktrees/<name>`, which lives *outside* the worktree. Mounting only the worktree therefore breaks git inside the container — verified: `fatal: not a git repository: (null)`.

Four approaches were tested (Appendix B):

| Approach | Host | Container |
|---|---|---|
| Mount the worktree alone | ✅ | ❌ |
| Rewrite `.git` to a container path | ❌ **breaks the host** | ✅ |
| Mount the gitdir at its literal host path | ✅ | ✅ but leaks host layout |
| **Relative pointers + mount the whole root** | ✅ | ✅ |

The last is the design. Both pointers — the worktree's `.git` and the reverse `gitdir` file in the root — are rewritten to **relative** paths, and the container mounts the entire repo root with the worktree as its working directory:

```bash
docker run -v ~/.claudio/repos/<repo>:/repo -w /repo/worktrees/<id> ...
```

Verified end to end: a commit made inside the container appears immediately on the host, with host git fully functional throughout. Rewriting the pointer to a container-only path is the tempting shortcut and it **breaks the host** — one `.git` file cannot hold two paths, so relative pointers are the only arrangement that satisfies both sides at once.

**Dependency installation is declared, never assumed.** Provisioning runs no implicit `npm install`; a repo that needs one says so:

```yaml
# .claudio.yml
post_create:
  - npm ci
```

This keeps `create` fast and predictable, and keeps the tool from guessing what a project's setup step should be (§12.4).

`home/` is bind-mounted to `/home/agent`. This makes the Claude Code session's own state — conversation transcripts, settings, shell history — durable across container rebuilds and readable from the host.

### 5.2 Dependency directories: bind-mounted, not offloaded

Heavy dependency directories (`node_modules`, `.venv`, `target/`) are the worst case for a bind mount: hundreds of thousands of small files that the toolchain then `stat()`s constantly. The obvious optimization is to put them in a Docker named volume, which is genuinely much faster.

**This was investigated in depth and rejected.** The full evidence is in Appendix A; the summary is:

- A named volume is **7–15× faster** on metadata operations (`stat` walks, `grep -r`, `rm -rf`) — measured on this machine, not assumed.
- On OrbStack the host *can* see into a volume (`~/OrbStack/docker/volumes/<name>`), and a host symlink into it round-trips correctly in both directions. On Docker Desktop it cannot: volumes live inside one opaque disk image.
- But the container must *also* mount the volume at that path, because the host symlink stores an absolute macOS path that dangles inside the container. And a fresh volume's root is `0:0`, so the non-root agent gets `Permission denied` until it is chowned.

The decision rests on a simpler point than any of that detail: **offloading's cost is that the host cannot properly see the offloaded directory**, and host access to the working tree is the whole reason this project exists. A tool built to give the host first-class access should not ship a feature whose price is taking it away. The target runtime already has fast bind mounts, and the feature would carry a runtime-conditional code path, chown handling, symlink retry logic, `EXDEV`/pnpm interactions, and case-sensitivity divergence between APFS and ext4.

So: **bind-mount everything, dependency directories included.** If dependency I/O ever becomes a measured bottleneck, the evidence to revisit this is preserved rather than lost.

For repos that genuinely feel the cost, the better answer is upstream: Yarn PnP (or pnpm's `node-linker=pnp`) replaces ~100k small files with one zip per package and uses no hardlinks at all, attacking the root cause instead of routing around it.

### 5.3 What the host gets

The host has a real directory at `~/.claudio/repos/<repo>/worktrees/<instance-id>`:

- Open it in any editor or IDE; no remote-container extension required.
- Run host `git` against it — `diff`, `log`, `add -p` all work natively.
- `claudio cd <id>` prints the path; a shell function wraps it for `cd $(claudio cd <id>)`.

This is the concrete answer to the "host access to the cloned repo folder" requirement: it is not exported, synced, or copied out — it *is* the working tree, and the container is the thing mounting it.

---

## 6. Port forwarding

### 6.1 Detection

The orchestrator determines which ports a repo's apps and services listen on, in precedence order (later overrides earlier):

1. **Framework defaults by project type** — detected from manifest files.
   | Signal | Inferred service | Port |
   |---|---|---|
   | `next.config.*` | web | 3000 |
   | `vite.config.*` | web | 5173 |
   | `package.json` w/ `react-scripts` | web | 3000 |
   | `manage.py` (Django) | web | 8000 |
   | `Gemfile` + `config.ru` (Rails) | web | 3000 |
   | `go.mod` + `net/http` | api | 8080 |
   | `Cargo.toml` + `axum`/`actix` | api | 8080 |

2. **`docker-compose.yml` / `compose.yaml` in the repo** — parse the `ports:` and `expose:` keys of every service. This is the highest-signal source, because it is the repo's own declaration of what it serves. Services here are also candidates to run as sidecar containers (§6.4).

3. **`package.json` scripts / Procfile / Makefile** — regex for `--port N`, `-p N`, `PORT=N`.

4. **`.env`, `.env.example`** — `PORT=`, `APP_PORT=`, `DATABASE_URL=postgres://…:5432/…`.

5. **`.claudio.yml` in the repo** — explicit declaration, always wins:
   ```yaml
   ports:
     - name: web
       container: 3000
     - name: api
       container: 8080
     - name: db
       container: 5432
       expose: false      # container-internal only, not forwarded to host
   ```

6. **Runtime discovery** — a lightweight watcher inside the container polls `/proc/net/tcp{,6}` for new `LISTEN` sockets. When the agent starts a server on an undetected port, the daemon can bind it on demand (§6.3).

Detection is a *ranked guess*; every mapping records its `source` so `claudio ports <id>` can show the user why a port is mapped and let them correct it with `--add` / `--remove`.

The `--ports` flag on `create` **supplements** detection rather than replacing it, and wins on conflict (recorded as `source: manual`). Wholesale override would force re-declaring every port just to add one; supplementing matches the actual case — "the detected ports are right, I also want the debugger exposed."

`--ports` names **container ports only** (`--ports 9229`, not `9229:9229`). The host side is never the caller's to choose: §6.2's first-free-in-range allocation is what allows a second instance of the same repo to exist at all, so pinning a host port would reintroduce precisely the collision that design eliminates. The `container:host` form is rejected with an error naming the bare form, rather than accepted-and-ignored — a syntax that reads as a pin but silently does nothing is worse than one that refuses.

### 6.2 Host port allocation

Static mapping (container 3000 → host 3000) breaks the moment a second instance exists. Instead:

- A **host port range**, default `43000–43999`, configurable as a fallback when the default does not suit the machine (conflicting local services, corporate port policy). Exhausting it is a clear error naming the range and how to widen it, not a cryptic bind failure.
- Allocation is *first-free-in-range*, recorded in SQLite, and verified by an actual `bind()` probe on `127.0.0.1` before being committed — this catches ports held by non-Claudio processes.
- Allocations are released on `stop` and `destroy`. A crashed daemon's allocations are reclaimed on startup by reconciling against live containers.
- Bindings are to `127.0.0.1` by default, **not** `0.0.0.0` — a sandboxed agent's dev server should not be exposed to the local network. `--publish-all-interfaces` opts out.

The user sees a stable table:

```
$ claudio ports brave-otter
SERVICE   CONTAINER   HOST                    SOURCE
web       3000        http://127.0.0.1:43001  detected (next.config.js)
api       8080        http://127.0.0.1:43002  declared (.claudio.yml)
postgres  5432        —  (not exposed)        declared (.claudio.yml)
```

### 6.3 Dynamic port binding

Docker cannot add a port binding to a running container. Two options were considered:

- **(A) Recreate the container** when a new port appears. Correct but disruptive — it kills the Claude Code session.
- **(B) Userspace forwarder on the host.** The daemon runs a small TCP proxy: it listens on the allocated host port and dials the container's IP:port directly over the Docker bridge network.

**Decision: (B), with (A) available as `claudio rebind <id> --recreate`.**

Rationale: the container's IP is reachable from the daemon directly on Linux, and on macOS through the runtime's VM network (OrbStack routes container IPs to the host natively; Docker Desktop requires the proxy to dial via the VM). Option B lets a port appear seconds after the agent starts a server, with no session loss. The proxy is ~100 lines (`net.Listen` + `io.Copy` both ways) and its lifetime is tied to the instance.

To keep the common case fast, the *pre-detected* ports from §6.1 are bound natively by Docker at container creation; only *runtime-discovered* ports use the proxy.

### 6.4 Compose-based repositories

If the repo has a `docker-compose.yml`, the instance is not a single container but a **compose project**:

- The daemon generates an override file that
  - joins all services to a per-instance network `claudio_<id>`,
  - rewrites every published port to the daemon-allocated host port,
  - adds the Claudio agent container as an extra service on the same network,
  - bind-mounts the workspace into the agent container.
- The agent container reaches `postgres:5432` by service name, exactly as the repo expects.
- `docker compose -p claudio_<id>` namespaces everything, so N instances of the same repo coexist.

This is the main reason the port allocator must rewrite rather than pass through: two instances of the same compose file would both want host 5432.

**Services are sidecars; client tools are in the image.** Postgres, Redis, and Elasticsearch run as their own containers with their own lifecycle and per-instance isolation. What goes *into* the agent image is the tooling that talks to them — `psql`, `redis-cli`, native build dependencies — declared through §7.1's YAML. The agent container should never run the service it is developing against.

**Docker-in-Docker is not the mechanism.** Nesting a container runtime inside the agent container is explicitly rejected: mounting the host Docker socket is a direct host-root escalation path (§7.4). A repo that genuinely needs the agent to run `docker compose` itself gets a rootless DinD sidecar, opt-in per instance.

A repo may also declare services in `.claudio.yml` without shipping a compose file at all; these are synthesized into the same per-instance project:

```yaml
services:
  - name: db
    image: postgres:16
    env:
      POSTGRES_PASSWORD: dev
  - name: cache
    image: redis:7
```

Sidecar lifecycle is coupled to the instance: `stop` stops the project, `destroy` removes it including sidecar volumes, health appears in `claudio status`, and `claudio logs <id> --service db` reaches sidecar logs — an agent blocked on a database that failed to start should be diagnosable without dropping to `docker ps`.

---

## 7. The container

### 7.1 Image strategy

**Base image** (`claudio/base`) — `FROM node:22-slim`, plus git, curl, ripgrep, tmux, `tini`, a non-root `agent` user, and the Claude Code CLI. Node is required for Claude Code itself regardless of what the repo needs, so starting from the official Node image beats installing Node onto bare Debian.

**Node-only to start.** Rather than maintaining a matrix of prebuilt toolchain images, the image builder is **driven by YAML**, so additional dependencies are declarative:

```yaml
# .claudio.yml
image:
  base: node:22-slim
  apt:
    - libpq-dev
  npm_global:
    - pnpm
```

The builder generates a Dockerfile from this. Adding Python, Go, or Rust later becomes a config change rather than a code change, and repos with unusual system dependencies do not need Claudio to ship an image for them.

**UID/GID matching is a build arg, not a baked constant.** The image is built with `USER_UID`/`USER_GID` matched to the host user (here `501:20`, against Debian's default `1000:1000`) and cached per host — the same approach devcontainers take. Since `/workspace` is a bind mount, every file the agent creates carries the container's UID; OrbStack auto-maps ownership so a mismatch mostly works there, but it fails differently on Docker Desktop and native Linux. The failure this prevents is root-owned files appearing in the user's repo that need `sudo` to remove.

**Repo layer (optional)** — if the repo has `.devcontainer/Dockerfile` or `.claudio/Dockerfile`, it is built `FROM` the resolved base as an escape hatch. An explicit `image.dockerfile` path in `.claudio.yml` takes precedence over both. The escape hatch **replaces** the generated Dockerfile rather than layering beneath it: a repo that supplies its own Dockerfile owns the whole build.

**Building is explicit, never a side effect of `create`.** `claudio image build [--repo <path>]` is its own verb; `create` only *resolves* which image an instance needs and fails with an actionable error naming the exact build command when it is missing. A multi-minute, network-dependent Docker build must not happen as a surprise inside what the user asked to be a fast provisioning step. The per-repo tag is derived from the repo's identity (its `origin` remote where parseable, else a `file://` path form), so `image build --repo <clone>` and a later `create <same-url>` land on the same tag without either command knowing about the other.

Devcontainer compatibility is a deliberate goal: `.devcontainer/devcontainer.json` already encodes image, features, forwarded ports, and post-create commands. Where it exists, Claudio reads it and treats it as a higher-precedence source than its own detection.

### 7.2 Process model inside the container

PID 1 is **`tini`**, with a small supervisor as its child — this gets zombie reaping and signal forwarding for free rather than hand-rolling `SIGCHLD` handling. Claude Code is not PID 1. The supervisor starts:

| Process | Purpose |
|---|---|
| `sshd`-free **agent-bridge** | Small HTTP/JSON server on a container-internal port. Health, port discovery, exec requests from the daemon. |
| **tmux server** | Hosts the session named `claude`. |
| **Claude Code** | Launched inside the tmux session, cwd `/workspace`. |
| **port-watcher** | Polls `/proc/net/tcp` and reports new listeners to the daemon via agent-bridge. |

Claude Code runs inside tmux rather than as PID 1 so that:
- The user can attach and detach without signalling the process.
- Multiple viewers can attach to the same session simultaneously.
- The session survives a client disconnect (SSH drop, laptop sleep, terminal close).
- Scrollback is preserved and greppable.

The pane runs `while true; do claude && break; bash -l; done` rather than `claude` alone (ROD-116). The pane's process is the session's only process, so its exit status decides the session's fate — which is exactly the control wanted.

Quitting Claude Code deliberately (a double Ctrl-C, which exits 0) breaks the loop, so the pane exits, tmux tears the session down, the `docker exec` behind `claudio attach` returns, and the user lands back on their **host** shell. That is what "closing a session" should mean, and it is why the loop is conditional.

A nonzero exit — a crash, a bad credential — falls through to an interactive shell instead. The session stays up with a pane the user can type into, so a broken instance is something to attach to and debug rather than one that silently disappeared; leaving that shell retries Claude Code.

The original bug was the unguarded version of this: with a bare shell as the pane's process, exiting it destroyed the session and, this being the only session, the whole tmux server. The container stayed `Up` regardless (the entrypoint's `tail -f` holds it open, not tmux), so `claudio ls` kept reporting a healthy instance that `claudio attach` could no longer reach.

Two alternatives were tried and rejected. `remain-on-exit on` keeps the session but leaves a *dead* pane, stranding a user who quits while attached on "Pane is dead" with no way to type — worse than the bug it fixed. An unconditional `while true; do claude || bash -l; done` relaunches Claude Code the instant it is quit, so there is no way out of the session at all.

### 7.3 Fast provisioning

Cloning a large monorepo per instance is slow. Mitigations:

- **Worktrees, not repeated clones (§5.1).** The repo is cloned once per root; every subsequent session is a `git worktree add` sharing the same object store. Provisioning drops from minutes to seconds without any reference-clone machinery.
- **Shared package caches.** `~/.claudio/cache/{npm,pip,cargo,go}` is bind-mounted read-write into every container at the toolchain's cache path. Installs in instance N+1 hit a warm cache.
- **Image prewarming.** The daemon keeps the last-used toolchain images pulled.

### 7.4 Sandbox posture

The container is a **security boundary against accident, and a partial boundary against malice.** Explicitly:

- Non-root `agent` user; no `--privileged`; `--cap-drop=ALL` plus only what the toolchain needs.
- `--security-opt no-new-privileges`.
- Read-only root filesystem where the toolchain tolerates it, with `tmpfs` for `/tmp`.
- Memory and CPU limits per instance — **6 GB / 4 CPUs** on this machine, set globally and overridable per repo, and overridable again locally with `claudio create --memory M --cpus N --pids N` (§12.3's three-layer resolution: global < repo < local). `create` reports when a local override changes what the repo's own `.claudio.yml` requested, rather than substituting a different number silently. Sized against the **OrbStack VM's 15.7 GB**, not the host's 36 GB: the VM cap is what containers actually share, and sizing against host RAM overcommits by more than 2×.
- PID limit (512) to contain fork bombs.
- **An exceeded limit must be legible.** Docker exposes `OOMKilled` in container state; `claudio status` reports "killed: out of memory (limit 6g)" with the command to raise it, rather than a bare `STOPPED`. A limit that produces a baffling failure is worse than no limit — the reconciler would otherwise show a stopped container with no cause. `create` also warns when configured limits across running instances would exceed the VM's memory.
- **No Docker socket mount by default.** Mounting `/var/run/docker.sock` into an agent container is a host-root escalation path. Repos that genuinely need Docker-in-Docker get a rootless DinD sidecar, opt-in per instance.
- **Network egress policy.** Default: unrestricted (agents need npm, PyPI, GitHub, the Anthropic API). Optional `--network-policy=restricted` attaches the container to a network whose egress passes through a filtering proxy with an allowlist.
- **Credential scoping** — §8.

The honest caveat: a container is not a VM. A kernel exploit escapes it. For hostile code, the recommendation is a VM boundary (Lima/Colima with a dedicated VM per instance), which the architecture accommodates because the daemon talks to a Docker *endpoint*, not necessarily the local one (§10.2).

---

## 8. Credentials

Three distinct secrets, three different handling rules:

| Secret | Needed by | Approach |
|---|---|---|
| **Anthropic subscription token** | Claude Code in the container | One host-held credential from `claude setup-token`, injected at container start as `CLAUDE_CODE_OAUTH_TOKEN`. Stored in the host keychain (fallback `~/.claudio/auth/token`, `0600`). Never written to the workspace or baked into an image. Rotatable by restarting the instance. See §8.1. |
| **Git credentials** | `git clone`, `git push` | **Clone happens on the host**, using the host's existing git credentials — the container never needs them for provisioning. For agent-initiated pushes, a **credential proxy**: the container's `git` is configured with a credential helper that calls the daemon over the agent-bridge socket; the daemon decides whether to serve the credential, and can require interactive host-side approval for pushes. |
| **Repo secrets (`.env`)** | The app under test | Copied into the workspace at provision time from a host-side path the user names (`--env-file`). Never committed; `.claudio/` is added to a local `.git/info/exclude`. |

The credential proxy is the important one: it means an agent can push a branch without ever holding a token it could exfiltrate, and every push is attributable and optionally gated.

### 8.1 Anthropic authentication

The credential is a **subscription token**, not a Console API key. `claude setup-token` converts an existing Claude subscription into a long-lived token, so instances bill against the subscription rather than metered API usage. (Verified on the development machine: `claude auth status` reports `authMethod: claude.ai`, `subscriptionType: max`, with no `ANTHROPIC_API_KEY` set.)

Flow: on first `claudio create`, if no credential is stored, run `claude setup-token` interactively and capture it — a one-time setup step. Store it in the host keychain, and inject it into each container's environment at start.

Two placement rules matter:

- **Not under `~/.claude/`.** Claudio must not write into Claude Code's own config tree; a prune or rewrite there would take the credential with it. `~/.claudio/` is already the state directory.
- **Not per-instance files.** An earlier design minted a credential per container into `~/.claudio/auth/instances/<id>/`. Rejected: `setup-token` takes no arguments — no scope, no expiry, no label — and mints *one* long-lived token, so per-instance directories would hold N copies of the same secret. A file on disk is also no less readable to the agent than an environment variable; the separation buys lifecycle tidiness, not isolation, while adding cleanup paths that leak credentials when missed.

**Known limitation, accepted for phase 1:** this is the same credential in every container. An agent that reads it holds the user's subscription token, and revoking it kills every instance at once. §8's credential proxy is the actual fix — the container holds nothing and the host adds auth to outbound requests — and Anthropic auth should be folded into that same broker alongside git rather than built as a second mechanism.

---

## 9. Host ↔ session interaction

### 9.1 Attach (primary path)

```
$ claudio attach brave-otter
```

Execs `docker exec -it claudio-brave-otter tmux new-session -A -s claude`. The user gets the real Claude Code TUI with full fidelity: colors, resize, Ctrl-C, the lot. Detach with the tmux prefix (`Ctrl-b d`) — the session keeps running.

`new-session -A` rather than `attach -t` so that attach is self-healing (ROD-116): it attaches when the session is there and creates it when it isn't. A container's tmux session can be gone while the container itself is still `Up` — tini and `tail -f` hold the container open independently of tmux — and `attach -t` in that state failed with tmux's bare "no sessions" plus Docker's generic "try docker debug" hint, a dead end mid-workflow.

Both `attach` and `logs` exec with `DOCKER_CLI_HINTS=false`. On exit from an interactive `docker exec -it`, the Docker CLI prints a "What's next: Try Docker Debug ..." promo; because these commands replace the Claudio process outright, that text arrives in the user's terminal as though Claudio had printed it — quitting a session ended with an unprompted `docker debug claudio-<id>` suggestion that is not a Claudio workflow and reads as an error where none occurred. It is prepended rather than appended so a user who sets the variable themselves still wins.

The recreate path passes the same pane command the entrypoint uses (`session.PaneCommand`, §7.2), so a session rebuilt by `attach` comes back running Claude Code rather than dropping the user at a bare container shell. tmux applies that command only when `-A` actually creates the session and ignores it when attaching to an existing one, so an ordinary attach is unaffected.

The CLI does *not* proxy this through the daemon. Inserting a daemon hop between two TTYs adds latency and breaks window-resize propagation for no benefit.

### 9.2 Rebuilding an instance onto a new image

`claudio rebuild <id> [--fresh]` builds the image, then re-provisions the instance's container from it — `claudio image build` followed by `claudio restart`, as one verb.

It exists because a change to the image — `image/entrypoint.sh` most of all — reaches a running instance through neither of the routes an operator reaches for first. Rebuilding the CLI does nothing, since the entrypoint ships in the image rather than the binary. `claudio restart` re-provisions from whatever image is tagged *now*, but never rebuilds it, so a stale tag is faithfully reused. Both commands report success, and the instance keeps running the old entrypoint.

That failure is silent, which is what makes it worth a command: `claudio ls` shows a healthy instance, the CLI is current, and nothing anywhere says the container predates the image. The fix was found only by comparing image IDs by hand (ROD-116). Pairing the two steps in one verb means the operator does not have to know the pairing to get it right.

The summary line reports the drift the rebuild closed (`Image 4e1269969011 -> 98e847299a9f`), and says so explicitly when the image did *not* change — distinguishing "the fix isn't in the image" from "the fix is in, look elsewhere" is the whole diagnostic value.

`--fresh` discards `home/` exactly as it does for `start`/`restart`; the default resumes the existing session (§4.1).

### 9.3 Non-interactive control

For scripting and the web UI, the daemon exposes:

```
claudio send <id> "run the test suite"     # inject a prompt into the session
claudio status <id>                        # state, ports, health, last activity
claudio exec <id> -- <cmd>                 # one-off command in the container
```

`claudio logs` (§6.4, §11) ships earlier than the rest of this section: phase 1 already has it as a plain `syscall.Exec` into `docker logs`/`docker compose logs`, no daemon involved — the same "no hop between the CLI and Docker" reasoning as `attach` (§9.1). What the daemon adds later is aggregation across sessions and a stream the web UI can subscribe to without shelling out itself.

`send` writes to the tmux pane via `tmux send-keys`, which is how the session receives input regardless of whether a human is attached.

### 9.4 Activity and attention

The most valuable signal in a multi-agent setup is *which session needs me*. State comes from **Claude Code hooks**, not from screen-scraping — pattern-matching `tmux capture-pane` output would break whenever the TUI changes, whereas hooks are a supported interface.

The hook events distinguish two genuinely different kinds of blocked:

| Event | Matcher | State |
|---|---|---|
| `Notification` | `permission_prompt` | Blocked on a permission decision |
| `Notification` | `idle_prompt` | Turn finished, awaiting the next prompt |
| `UserPromptSubmit` | — | → working |
| `Stop` | — | Turn complete |

That distinction matters for ranking: a permission request is usually urgent, a finished turn often is not. Hooks are configured as `type: "http"` posting to the container's agent-bridge, carry `session_id` in every payload, and fire in headless tmux with no client attached — which is precisely the case when nobody is looking.

Two constraints on the hook config: `timeout: 5` and `async: true`. Hooks default to a *ten-minute* timeout, so a slow agent-bridge could otherwise stall the very session it is meant to observe. The monitor must never be able to block the agent.

A pane-quiescence heuristic remains as a **fallback** for cases hooks miss (crash before `SessionEnd`, failed delivery, bridge down). It reports `IDLE (unconfirmed)` rather than asserting a state that was never actually observed.

`claudio ls` sorts blocked instances first, permission-blocked above idle. This turns "check on five terminals" into "answer the one that is actually blocking":

```
$ claudio ls
ID           NAME          BRANCH        STATUS   ATTENTION         PORTS
brave-otter  auth-refactor feat/auth     running  NEEDS PERMISSION  43001,43002
wise-heron   docs          fix/links     running  awaiting prompt   43004
calm-finch   perf-audit    main          running  working           43003
```

### 9.5 Web UI (optional, phase 2)

The daemon serves an HTTP + WebSocket endpoint. The UI is a dashboard of instance cards (status, attention, port links) with an embedded `xterm.js` terminal per instance, bridged to the same tmux session over a WebSocket. Port links are clickable, opening the forwarded `127.0.0.1:PORT` directly.

---

## 10. State, reconciliation, and failure

### 10.1 State store

SQLite at `~/.claudio/state.db`, WAL mode. Multi-writer in phase 1 (N CLI processes), single-writer once the daemon exists — see §12.5 for why the concurrency design must assume the former.

**The governing rule: SQLite stores *intent*; Docker stores *reality*.** Anything Docker already knows authoritatively is derived at query time, never duplicated — two sources of truth drift.

`instances` — the intent record:

```sql
id              TEXT PRIMARY KEY   -- "brave-otter", short and human-typeable
name            TEXT UNIQUE
repo_url        TEXT
branch          TEXT
commit_sha      TEXT               -- resolved at provision time
workspace_dir   TEXT
image           TEXT
container_id    TEXT               -- a pointer INTO Docker, not a copy of its state
runtime_profile TEXT               -- orbstack | docker-desktop | native | generic
desired_state   TEXT               -- running | stopped | destroyed
provision_step  TEXT               -- resume point if interrupted
created_at      INTEGER
last_active     INTEGER
```

Note `desired_state`, **not** `state`. Whether the container is actually running is Docker's answer, read live. Storing observed state is what creates drift; storing desired state is what the reconciler compares against.

`port_mappings` — the reservations, and the one thing Docker genuinely cannot tell you:

```sql
instance_id    TEXT
container_port INTEGER
host_port      INTEGER
protocol       TEXT               -- tcp | udp
service_name   TEXT               -- "web", "api", "postgres"
source         TEXT               -- detected | declared | manual
detected_from  TEXT               -- "next.config.js" — so `claudio ports` explains itself
UNIQUE(host_port)
```

This table is what earns SQLite in phase 1: host port ownership *across* instances is state nothing else tracks, and `UNIQUE(host_port)` is what makes concurrent allocation safe.

`repos` — reference-clone bookkeeping (mirror path, last fetch) for §7.3. `events` — an append-only log of state transitions, provisioning failures with the underlying error text preserved, and port allocations; this is what lets `claudio status` explain *why* something failed rather than only that it did.

**Deliberately not stored:** container running/exited status, container IP, image digests, anything from `docker inspect`. `claudio ls` is "select instances → ask Docker about their containers → merge", which is correct by construction when a container is killed out-of-band, rather than depending on reconciliation to notice.

A `schema_version` table and numbered migrations exist from the first commit — this store outlives every container.

The reconciler resolves disagreements between the two:

- Container gone, DB says desired `running` → mark stopped, emit event, release proxy ports.
- Container running, no DB row, but carrying Claudio labels → **flag as untracked; never auto-adopt or auto-remove** (below).
- Container running, no Claudio labels → ignore entirely; it belongs to something else.
- DB says PROVISIONING at startup → resume the provisioning state machine from `provision_step`.

**Untracked containers are surfaced, not resolved automatically.** If the DB is lost, restored from backup, or edited out of band, a labelled container can outlive its instance row. Reconstructing it automatically risks resurrecting an instance the user deliberately discarded; removing it automatically destroys work. Both are recoverable, so `claudio ls` lists them in a separate section and offers the two explicit moves:

```
UNTRACKED (1)
?   —   acme/webapp   running   container claudio-wise-heron · created 3d ago
        `claudio adopt <container>` or `claudio forget <container>`
```

`adopt` reconstructs the row from labels and re-reserves the published ports; `forget` removes the container.

Every container is labelled (`claudio.instance.id`, `claudio.repo`, `claudio.created_at`) so state is recoverable from Docker alone if `state.db` is lost.

### 10.2 Remote Docker endpoints

The daemon talks to a Docker endpoint via `DOCKER_HOST`. Nothing in the design assumes it is local. This makes two later capabilities cheap: running instances on a beefier remote machine, and per-instance VM isolation via Lima/Colima. The one thing that *does* assume locality is the bind mount — a remote endpoint requires either a synced workspace or accepting volume-only workspaces. Flagged as a known limitation.

### 10.3 Failure modes

| Failure | Behavior |
|---|---|
| Daemon crashes | Containers keep running (they are not children of the daemon). On restart, reconcile from Docker labels + SQLite. Port proxies are re-established. |
| Container OOM-killed | Reconciler sees the exit, marks `STOPPED`, surfaces the exit code and last log lines in `claudio status`. |
| Clone fails | Instance → `FAILED` with the git error preserved. Workspace kept for inspection unless `--clean-on-fail`. |
| Host port taken between reserve and bind | Allocator retries with the next free port; the bind probe makes this rare. |
| Docker daemon down | CLI reports it plainly and does not hang; instance state is preserved as last-known. |
| Disk pressure | `claudio gc` prunes destroyed instances, orphaned repo roots, stale worktree metadata (`git worktree prune`), and dangling images. A soft quota warns before provisioning when free space is low. |

---

## 11. CLI surface

```
claudio create <repo> [--branch B | --new-branch B] [--name N] [--env-file F] [--ports c,...]
                      [--publish-all-interfaces] [--memory M] [--cpus N] [--pids N]
                      [--clean-on-fail] [--yes]
claudio ls [--all] [--json]
claudio attach <id>
claudio status <id>
claudio logs <id> [--service X] [--follow]
                                         # single-container: streams its container's own
                                         # logs; compose instance: --service names one
                                         # sidecar (or the agent), omitted means every
                                         # service interleaved. No daemon needed — execs
                                         # straight into `docker logs`/`docker compose logs`.
claudio ports <id> [--add c] [--remove c]
claudio stop|start|restart <id> [--fresh]
claudio rebuild <id> [--fresh]           # rebuild the image, then recreate the container
                                         # from it — the one path that carries a changed
                                         # image/entrypoint.sh into a running instance
claudio destroy <id> [--keep-workspace]
claudio cd <id>                          # prints the workspace path (shell fn wraps it)
claudio adopt <container>                # reconcile an untracked container
claudio forget <container>               # remove an untracked container
claudio image build [--repo <path>]      # build claudio/base:latest, plus a repo-specific
                                         # layer when --repo's .claudio.yml declares one

# phase 2+
claudio send <id> <prompt>
claudio open <id> [--service web]
claudio exec <id> -- <cmd...>
claudio gc
claudio daemon [start|stop|status]
```

**Identity:** the generated ID (`brave-otter`) is permanent and canonical; `--name` sets an optional global-unique alias that resolves to it, and both work anywhere an `<id>` is accepted. Keeping the generated ID canonical is what stops the default branch (`claudio/<instance-id>`) and container names drifting when an instance is renamed. Unambiguous prefixes are accepted.

`claudio cd` matters more than it looks: the answer to "how do I get at the files" should be one command, not a path the user has to remember.

---

## 12. Technology choices

| Decision | Choice | Why |
|---|---|---|
| Language | **Go** | Single static binary, first-party Docker SDK, good concurrency for the reconciler and N port proxies, trivial cross-compilation. Available on this machine (1.24.5). See §12.2 for the Rust comparison. |
| Process model | **CLI-first; daemon deferred to phase 2** | Nothing in phase 1 needs a long-lived process. See §12.5. |
| Container runtime | **Any Docker-API-compatible engine** | The development machine runs **OrbStack**; Docker Desktop and native Linux must also work. Targets the Docker Engine API and probes the runtime at startup (§12.1). |
| Container control | **Docker Engine API** via the official SDK | Direct API is more precise than shelling out; the event stream is required for the reconciler. |
| IPC (phase 2+) | **HTTP + JSON over a Unix domain socket** | Debuggable with `curl --unix-socket`, no protoc in the build, and `logs --follow` works as chunked streaming/SSE. Local-only; file permissions are the access control. |
| State | **SQLite** (`modernc.org/sqlite`, cgo-free) | Durable, transactional, zero-ops, keeps the static-binary property. |
| Session multiplexing | **tmux** | Battle-tested detach/reattach, multi-viewer, scrollback. Reimplementing it would be the single biggest source of bugs. |
| Config | **YAML** (`.claudio.yml`) — Claudio's own schema | Devcontainer and compose files are read as *inputs* and translated (§12.4), but the model is Claudio's own; borrowing their shape would fight the machine/project layering. |

---

### 12.1 Runtime detection

The engine's identity changes the correct mount strategy (§5.3), so the CLI probes it via `/info` and records a **runtime profile**:

| Detected | Profile | Consequence |
|---|---|---|
| `OperatingSystem` contains `OrbStack` | `orbstack` | Fast native bind mounts; host-visible volume paths (§5.3). |
| `OperatingSystem` contains `Docker Desktop` | `docker-desktop` | VirtioFS bind mounts; volumes live inside the VM disk image and are **not** host-traversable. |
| Neither marker, and the CLI process itself runs on `GOOS=linux` | `native` | Bind mounts are ordinary kernel mounts; no penalty, no indirection. |
| Anything else | `generic` | Conservative defaults: bind-mount everything, no volume tricks. |

The profile selects mount defaults and gates features that depend on host-visible volumes. It is recorded on each instance, so an instance created under one runtime is not silently reused under another.

**Two implementation findings, both empirical and both fixed in code before shipping:**

1. **Native Linux cannot be detected from the `OperatingSystem` string alone.** A native Linux daemon reports its *distro name* — `"Ubuntu 22.04.3 LTS"`, `"Debian GNU/Linux 12 (bookworm)"` — and Ubuntu's own string does not contain the substring "linux" at all. The only reliable native-Linux signal is that the CLI process itself is running on `GOOS=linux` with neither VM marker present; the daemon's OS string cannot carry this distinction on its own. An unrecognized OS string reached from a non-Linux host (e.g. a remote Linux daemon dialed from macOS, §10.2) is `generic`, not `native` — those are different situations.

2. **The endpoint must be resolved through the `docker` CLI's current context, not left to the SDK's default.** Verified on the development machine: the OS-level default socket (`/var/run/docker.sock`) was symlinked to **Docker Desktop's** socket, even though `docker info` and every `docker` command correctly used **OrbStack** — because the `docker` CLI itself reads `currentContext` from `~/.docker/config.json`, and the Docker Go SDK's `client.FromEnv` does not. Resolving the endpoint via `docker context inspect` before falling back to the SDK's default is what makes detection agree with what the user's own `docker` commands actually do; skipping this step silently misclassified `orbstack` as `docker-desktop` on this exact machine.

### 12.2 Go vs Rust

Rust was considered seriously; the call was roughly 60/40.

**For Go:** `github.com/docker/docker/client` is *first-party* — Docker's own daemon and CLI are written in Go, so the API types **are** the Go types. Rust's `bollard` is good and actively maintained, but it is a third-party reimplementation tracking someone else's API. For a tool whose entire job is orchestrating Docker, that asymmetry matters more than it usually would. Goroutines also fit the workload (N port proxies, an event subscriber, a reconciler loop) with less ceremony than `Arc<Mutex<…>>` plumbing.

**For Rust:** better error modelling for this domain — provisioning is a state machine with a dozen distinct failure modes, and `Result` + `thiserror` expresses "which of these went wrong" far more precisely than `if err != nil` chains, for a tool whose value depends on explaining failures clearly. `clap` is also ahead of anything in Go, and `rusqlite` is a nicer binding (with the cgo tradeoff reversed, since it bundles SQLite and still yields a static binary).

Both produce a single static binary and cross-compile cleanly; that is a wash. The decision rests on the first-party SDK, and on this being mostly *coordination* work — shelling to git, calling Docker, moving bytes between sockets — which is Go's sweet spot and is neither performance- nor memory-safety-critical.

### 12.3 Configuration layering

Configuration resolves in three layers, later winning:

```
global config  <  repo .claudio.yml  <  local per-instance override
   what this        what this            what this run
   machine allows   project needs        needs right now
```

The split is not arbitrary — it follows what each fact is *about*. Ports and services are properties of the **project**, so they belong in the repo and should be versioned and shared. Resource ceilings are properties of the **machine**, so their baseline is global: not every machine is the same, and not every repo has the same needs.

The global baseline applies to every new instance, whether created by cloning a repo or by `claudio create --new` on a fresh `git init` root. A repo that declares nothing simply inherits it.

**A repo's request must be overridable locally.** "This monorepo needs 12 GB to build" is worth committing; "my VM only has 15.7 GB" is a fact about the machine that has to win. Without the local layer, a repo committing a request larger than the available VM would make the instance unstartable with no recourse. When the local layer overrides a repo's request, `create` says so rather than silently ignoring it.

### 12.4 Config schema

Claudio defines its own schema rather than adopting `devcontainer.json`'s. The deciding cases were concrete: `forwardPorts` is a bare integer array with nowhere to express a service name or `expose: false` for a container-internal port, and `hostRequirements` runs the opposite direction from what is needed here — it states a *minimum the host must meet*, where Claudio needs a *ceiling the instance may not exceed*, layered over a machine-level baseline (§12.3). A schema shaped by a tool with one container per repo and no machine layer would have to be fought at every one of those points.

Conventions: **`snake_case` throughout**, no camelCase anywhere. Every list-shaped key is a list, never "string or list". Unknown keys are an error, not silently ignored — a typo in `post_create` should say so rather than quietly doing nothing.

#### `<repo>/.claudio.yml` — what the project needs

Versioned, shared, committed. Every key optional; a repo that declares nothing gets sensible defaults.

```yaml
image:
  base: node:22-slim          # default when omitted
  apt: [postgresql-client, libpq-dev]
  npm_global: [pnpm]
  dockerfile: .claudio/Dockerfile   # escape hatch; built FROM the resolved base

ports:
  - name: web                 # required; appears in `claudio ports`
    container: 3000           # required
  - name: api
    container: 8080
  - name: db
    container: 5432
    expose: false             # reachable from the agent, not published to the host

services:                     # sidecars; synthesized into the compose project
  - name: db
    image: postgres:16
    env:
      POSTGRES_PASSWORD: dev
    resources:                # sidecars get their own, smaller ceilings
      memory: 1g

post_create:                  # run once, inside the container, after mounting
  - npm ci

resources:                    # what THIS PROJECT needs; overridable locally
  memory: 10g
```

#### `~/.claudio/config.yml` — what the machine allows

```yaml
workspace_root: ~/.claudio    # where repo roots and instances live

resources:                    # the baseline every new instance inherits
  memory: 6g
  cpus: 4
  pids: 512

ports:
  range: [43000, 43999]       # fallback when the default does not suit
  bind: 127.0.0.1             # never 0.0.0.0 without --publish-all-interfaces

runtime:
  docker_host: ""             # empty = default endpoint
```

`resources` means the same thing in both files, which is what makes the §12.3 layering legible: the repo states a need, the machine states a limit, and the local layer settles it.

#### Interop

`.devcontainer/devcontainer.json` remains a first-class **input**. Where a repo has one it is read and translated into this schema (§7.1, ROD-110), so a repo that already declares its image, ports, and setup needs no `.claudio.yml` at all. Translating at the boundary keeps the interoperability without importing the spec's constraints into the model.

The same applies to `docker-compose.yml`: a repo that ships one has already declared its services, and Claudio uses it directly (§6.4) rather than asking for the same facts twice.

### 12.5 CLI-first, daemon-ready

Phase 1 ships **no daemon**. A daemon earns its place with the reconciler (§10.1), the port proxies (§6.3), and the activity monitor (§9.4) — all phase 2+. Building one for phase 1 would mean a serialization round-trip to reach code in the same address space.

The layering is what keeps that a deferral rather than a rewrite:

```
cmd/claudio/          flag parsing, output formatting, exit codes
internal/client/      client.go (interface) | local.go (phase 1) | remote.go (phase 2)
internal/core/        the operations — I/O-free
internal/store/       SQLite: transactions, migrations
internal/engine/      Docker API client + runtime profile
internal/session/     tmux: create, send-keys, capture-pane
```

Three rules make the migration cheap:

1. **`core` is I/O-free.** Structs in, structs out; it never prints, reads stdin, or calls `os.Exit`. All user-facing output lives in `cmd/`. This is the load-bearing constraint — violating it turns the daemon port into a rewrite.
2. **Commands depend on the `Client` interface**, never on `core` directly. Phase 1 wires `local.Client` (a thin pass-through); phase 2 wires `remote.Client` over the socket. Command files do not change. `main` selects via a socket-existence check, so the daemon is purely additive.
3. **`attach` sits outside all of it** — it resolves the container name and `syscall.Exec`s into `docker exec -it … tmux attach`, in both phases. Replacing the process is what yields a genuine TTY with working resize and signal handling; any abstraction here costs fidelity for nothing.

Interface discipline required from day one, so the eventual wire format is not an afterthought: request/response types are plain serializable structs (no `io.Reader` fields, func values, channels, or `*sql.Tx` leaking through); errors carry a **typed code**, not just a string; every operation takes `context.Context` first; and progress is a **caller-supplied sink** rather than a print — phase 1 passes a terminal writer, phase 2 the HTTP response stream, with the same `core` signature.

**Storage must be multi-process safe from the start.** Phase 1 has N concurrent CLI processes writing one SQLite file; phase 2 has a single writer. Designing for the multi-process case now yields code that stays correct when the daemon arrives — the reverse assumption breaks the moment two `claudio create` calls race for a port. Hence WAL mode, a short busy timeout, `BEGIN IMMEDIATE` on every read-modify-write, and port allocation as **one transaction** (`SELECT free → INSERT reservation → COMMIT`, then `bind()`-probe, releasing on failure) rather than select-then-insert across two statements.

**Known deferral:** with no daemon, nothing watches the Docker event stream, so state converges *lazily* — on the next command that touches an instance. That is the gap §10.1's reconciler closes in phase 2, and it is deliberate.

## 13. Phasing

**Phase 1 — Core loop.** `create`/`ls`/`attach`/`destroy`, bind-mounted clone, statically detected ports, SQLite state, tmux attach — **plus compose sidecars**. No daemon.

Sidecars are phase 1 rather than a later enhancement because most real repos need Postgres or Redis to run at all; an instance that cannot start the app it is sandboxing is not useful. The dividing line is that **services run as sidecars** on a per-instance network while **client tools** (`psql`, `redis-cli`) go in the agent image — the agent container holds the tools that talk to services, never the services themselves.

**Phase 2 — Ergonomics.** The daemon arrives here, because the port proxy needs a long-lived listening socket. Runtime port discovery, richer detection, the hook-based activity monitor, `send`, `open`, shared package caches, `gc`.

**Phase 3 — Devcontainers.** `.devcontainer.json` compatibility for repos that already declare image, ports, and setup commands.

**Phase 4 — Surface and scale.** Web UI, remote Docker endpoints, credential proxy with push approval, network egress policy, per-instance VM isolation.

---

## 14. Open questions

1. ~~**Instance ↔ branch coupling.**~~ **Resolved.** An instance is bound to one branch at creation. `--branch` checks out an existing branch, `--new-branch` creates one, and passing neither generates `claudio/<instance-id>`. The generated default is what keeps "one instance = one line of work" true and stops parallel agents colliding on a branch by accident. Nothing prevents the agent from switching branches inside the container; the binding is a default and a naming convention, not an enforcement.
2. ~~**Session persistence across container rebuild.**~~ **Resolved.** `claudio restart` **resumes** the existing session; `--fresh` opts out. Resuming is the payoff for bind-mounting `home/` at all — without it the mount buys nothing and containers are not really disposable. Contingent on verifying that Claude Code resumes reliably from persisted state in a *new* container (nothing session-critical outside `home/`); if it does not, the fallback is fresh-by-default with `--resume`, taken as an explicit decision rather than a silent degradation.
3. ~~**Concurrent attach.**~~ **Resolved by scope.** Claudio is **single-user and local-only** — no outside traffic, no shared instances, no second operator. Multiple tmux clients therefore attach to the same session with a shared cursor, and that is correct: one person attaching twice is either deliberate or immediately obvious to them. No read-only mirror, no `-r` mode, no "someone else is attached" warning — all of that would be machinery for a situation that cannot arise. A future web UI is a full client on the same session, not a mirror.

    This scope also underwrites two decisions made elsewhere: binding published ports to `127.0.0.1` rather than `0.0.0.0` (§6.2), and using a Unix socket with file permissions as the only access control (§12). There is no network exposure surface to reason about.
4. ~~**Resource defaults.**~~ **Resolved by measurement.** Conservative, overridable defaults: **6 GB memory, 4 CPUs, 512 PIDs** per instance (§7.4). The measurement that mattered: the host has 36 GB, but **OrbStack's VM is capped at 15.7 GB**, and the VM cap is the budget containers actually share — sizing against host RAM would overcommit by more than 2×. All 12 CPUs pass through, so the CPU figure is deliberate overcommit on the basis that builds are bursty. Adaptive per-instance limits were rejected: a ceiling that changes when a sibling starts is confusing to reason about.
5. ~~**`.claudio.yml` schema stability.**~~ **Resolved.** Claudio defines **its own schema** (§12.4), rather than adopting or half-borrowing `devcontainer.json`'s. Two concrete cases decided it: `forwardPorts` is a bare integer array with nowhere to express a service name or `expose: false`, and `hostRequirements` states a *minimum the host must meet* where Claudio needs a *ceiling the instance may not exceed* over a machine baseline (§12.3) — a spec built for one container per repo has no equivalent of that layering. Conventions: `snake_case` throughout, every list-shaped key always a list, unknown keys an error rather than silently ignored. `.devcontainer.json` and `docker-compose.yml` remain first-class *inputs*, translated at the boundary (§7.1, §6.4), so nothing already working is reinvented.
6. ~~**Runtime-conditional behavior.**~~ ~~**Detecting the editing model.**~~ **Both resolved by dropping volume offloading entirely (§5.2).** With dependency directories always bind-mounted there is no runtime-conditional mount path to support and no editing model to detect — the host always sees the full tree.

---

## Appendix A — Mount strategy evidence

All claims in §5.3 were verified on the development machine on 2026-09-05 rather than taken from documentation. Environment: OrbStack (server 29.4.0, client 28.3.2), macOS 26.5.2, Apple Silicon, 16 GB. Docker Desktop is installed but was not the active context — an easy misread, since `docker --version` reports the *client*.

**Verified on OrbStack**

- `~/OrbStack/docker/volumes/<name>` exposes named volumes to macOS; confirmed by OrbStack's own `~/OrbStack/README.txt`.
- Container→host, host→container, and both directions *through a host symlink* all round-trip correctly.
- A host symlink alone leaves `/workspace/node_modules` **dangling inside the container** (`readlink` returns the macOS path). The volume mount is required regardless.
- Volume root is `0:0` on creation; uid 1000 writes fail until it is chowned.
- After chown to `1000:1000`, the Mac user (uid 501) can still read and write through the host view — OrbStack does not enforce Linux ownership there.
- Host volume listing is eventually consistent; a direct `stat` resolves a volume that `ls` has not yet shown.

**Verified on Docker Desktop**

- `Mountpoint` points inside the VM and does not exist on macOS; all storage is one ~64 GB `Docker.raw`. Not traversable.

**Verified for mount shadowing generally**

- `mount` inside the container shows two distinct filesystems (`virtiofs` for the bind mount, `btrfs`/`ext4` for the volume).
- The shadowed host directory retains its prior contents — it goes **stale**, not empty.
- An empty volume is seeded from the **image layer only**: 36 files over Alpine's `/etc`, **0 files** over a bind-mounted `node_modules`.
- `ln` across the boundary fails with `EXDEV: Cross-device link`.
- `inotify` events propagate from host writes across the bind mount.

Benchmark methodology: 3000 small files, three runs each, comparing bind mount / named volume / container overlay. Results in §5.3.1. Corroborating published data: Mainardi's 2025 macOS Docker benchmarks (~2.5× on `npm install`; Docker-VZ 9.53 s vs 3.61 s hybrid).

Test volumes and directories were removed after measurement.

### Primary sources

- [VS Code — improve container performance](https://code.visualstudio.com/remote/advancedcontainers/improve-performance)
- [VS Code — remote extension architecture](https://code.visualstudio.com/api/advanced-topics/remote-extensions) (why the "harmless" caveat depends on workspace extensions)
- [Docker — volumes](https://docs.docker.com/engine/storage/volumes/)
- [Docker — Synchronized File Shares announcement](https://www.docker.com/blog/announcing-synchronized-file-shares/)
- [Mainardi — Docker performance on macOS, 2025](https://www.paolomainardi.com/posts/docker-performance-macos-2025/)
- [pnpm FAQ](https://pnpm.io/faq) · [pnpm#5318](https://github.com/pnpm/pnpm/issues/5318)
- [vscode-remote-release#3008](https://github.com/microsoft/vscode-remote-release/issues/3008) · [#6669](https://github.com/microsoft/vscode-remote-release/issues/6669) (root-owned leftover directory)
- OrbStack file sharing: `~/OrbStack/README.txt`, [orb.cx/docker-mount](https://orb.cx/docker-mount)

---

## Appendix B — Git worktrees across the container boundary

Verified on the development machine, 2026-09-06, because the failure mode is silent and the fix is not obvious.

**The problem.** `git worktree add` writes a `.git` **file** (not a directory) into the worktree, containing an absolute path to administrative files that live under the main clone:

```
$ cat worktrees/brave-otter/.git
gitdir: /Users/…/repos/acme-web/main-clone/.git/worktrees/brave-otter
```

That path is outside the worktree, so a container mounting only the worktree cannot resolve it:

```
$ docker run --rm -v <worktree>:/workspace alpine sh -c 'cd /workspace && git status'
fatal: not a git repository: (null)
```

There is also a **reverse pointer** — `main-clone/.git/worktrees/<name>/gitdir` points back at the worktree's `.git` file — so any fix has to keep both ends consistent.

**Four approaches tested:**

| Approach | Host | Container | Verdict |
|---|---|---|---|
| Mount the worktree alone | works | `fatal: not a git repository` | unusable |
| Rewrite `.git` to a container path | **breaks** (`fatal: not a git repository: /gitroot/…`) | works | unusable — one file cannot hold two paths |
| Mount the gitdir at its literal host path (`-v /Users/…:/Users/…`) | works | works | works, but leaks host layout into the container |
| **Relative pointers + mount the whole root** | works | works | **chosen** |

**The chosen arrangement.** Rewrite both pointers as relative paths:

```bash
echo "gitdir: ../../main-clone/.git/worktrees/brave-otter" > worktrees/brave-otter/.git
echo "../../worktrees/brave-otter/.git" > main-clone/.git/worktrees/brave-otter/gitdir
```

Then mount the repo root — not the worktree — with the worktree as the working directory:

```bash
docker run -v ~/.claudio/repos/<repo>:/repo -w /repo/worktrees/<id> …
```

Confirmed working end to end: host `git status` and `git rev-parse --abbrev-ref HEAD` both correct; inside the container the same commands report the same branch; a commit made **inside** the container appeared immediately in the host's `git log`.

**Also verified:**

- Git refuses to check out one branch in two worktrees — `fatal: 'claudio/brave-otter' is already checked out at '…'`. This is what makes "one instance = one line of work" structural rather than conventional.
- A UID mismatch triggers git's `safe.directory` protection; §7.1's build-arg UID matching is the primary defense, with a `safe.directory` entry in the image as a backstop.
