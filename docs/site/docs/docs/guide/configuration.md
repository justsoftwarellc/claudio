# Configuration

## Resource limits

Every instance gets a memory/CPU/PID ceiling, so a runaway build can't take down the machine. Defaults are 6 GB memory, 4 CPUs, 512 PIDs — sized against your container runtime's memory budget, not your host's total RAM. Override per-repo in `.claudio.yml`, or per-instance:

```bash
claudio create git@github.com:acme/web.git --memory 10g --cpus 6 --pids 1024
```

A flag wins over the repo's `.claudio.yml`, and `create` says so when it does. The value is written to `.claudio.yml`, so it survives `claudio restart` — restart takes no resource flags and re-derives every limit from that file.

An instance killed for exceeding its memory limit is reported as such by `claudio ls` and `claudio status`, distinct from a plain stop.

## `.claudio.yml` and `config.yml`

Two files, both local to your machine and neither committed:

- `.claudio.yml` — what this repo needs: ports, services, setup commands, instance IDs.
- `~/.claudio/config.yml` — what this machine allows: resource ceilings, port range, editor. Applies to every instance.

`~/.claudio/` (a directory) is Claudio's own state — repos, worktrees, the database.

### Where `.claudio.yml` lives

In the folder you ran `claudio create .` from, not in the worktree:

```
$ claudio create .
Created brave-egret (branch claudio/brave-egret)
Workspace: ~/.claudio/repos/github.com-acme-web/worktrees/brave-egret
Config: /Users/you/Projects/web/.claudio.yml      ← your folder
Ports: web:3000->43001
```

Edits take effect on the next `create` or `restart`. No commit required — Claudio reads the file on disk and adds it to `.git/info/exclude`.

An instance created from a remote URL has no folder of yours, so its config is read from the worktree. `create` prints that path too.

:::note Already committing a `.claudio.yml`?
Claudio still reads it from your folder, and tells you once:

```
note: .claudio.yml is tracked by git. Claudio treats it as local config,
      so your edits to it will keep showing up as changes.
      To make it local: git rm --cached .claudio.yml
```

Git keeps tracking a file it already tracks, so your edits show as changes until you untrack it. Claudio never touches your git index.
:::

`.claudio.yml` (all fields optional):

```yaml
image:
  base: node:22-slim       # default; override for a different toolchain base
  apt: [libpq-dev]         # extra apt packages baked into a per-repo image layer
  npm_global: [typescript]   # pnpm and yarn already ship in the base image
  dockerfile: .claudio/Dockerfile   # escape hatch: replaces the generated Dockerfile entirely

ports:
  - name: web
    container: 3000
    expose: false           # container-internal only; omit or true to publish to the host

host_services:              # services already on YOUR machine — see Host Services
  - name: mongo             #   inside the container: mongo:27017
    host: 27017             #   the port it already listens on (must be published)

services:                   # sidecars synthesized into a compose project — see Compose Sidecars
  - name: db
    image: postgres:16
    env:
      POSTGRES_PASSWORD: dev

post_create:                 # run ONCE, at create only — install dependencies here
  - npm ci

post_start:                  # run on EVERY start, in the background — run servers here
  - npm start

resources:
  memory: 10g               # this repo needs more than the machine default
  cpus: 6
  pids: 1024

instances:                  # written by Claudio; lets commands here skip the ID
  - brave-egret
```

`~/.claudio/config.yml` (all fields optional):

```yaml
workspace_root: ~/.claudio     # where repos/worktrees/state live
editor: code                    # what `claudio open` launches; see below
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

### `editor`

The editor `claudio open <id>` launches against an instance's worktree. Set it with the CLI rather than by hand — it checks the binary resolves on `PATH` before writing, so a typo fails immediately:

```bash
claudio config set editor code   # or cursor, subl, zed, nvim, …
claudio config get editor
```

A bare binary name or absolute path; arguments (`code -n`) aren't supported. There's no default — `install.sh` offers to set one. Only `claudio open` needs it.

### Lifecycle hooks: `post_create` vs `post_start`

The two hooks are not interchangeable. A Claudio restart **replaces the container** rather than restarting a process inside it, so work that persists on disk and work that has to be *running* have different schedules.

| | `post_create` | `post_start` |
|---|---|---|
| Runs on `create` | yes | yes |
| Runs on `start` / `restart` / `rebuild` | **no** | **yes** |
| Waits for the command to finish | yes | no — launched in the background |
| A failing command fails provisioning | yes | no (see below) |
| Use it for | `npm ci`, migrations, codegen | dev servers, workers, queue consumers |

The rule: if the command produces **files**, it belongs in `post_create`. If it produces a **process**, it belongs in `post_start`.

`npm start` in `post_create` blocks `claudio create` forever waiting for a server that never exits — and wouldn't come back after a restart anyway.

#### Running a dev server on every start

```yaml
ports:
  - name: web
    container: 3000

post_create:
  - npm ci                   # once: dependencies land in the worktree and persist

post_start:
  - npm start                # every start: the server has to be relaunched
```

`claudio create` installs dependencies and starts the server; `claudio restart <id>` brings the server back.

**The server must bind `0.0.0.0`, not `localhost`.** Published ports forward to the container's external interface, so an app on loopback is unreachable from the host even with its port mapped. Use `vite --host 0.0.0.0`, `next dev -H 0.0.0.0`, or `server.listen(port, '0.0.0.0')`. Prefer the explicit flag: `HOST=0.0.0.0` is ignored by some tools, Vite among them.

#### How `post_start` reports failures

`post_start` commands are launched detached, so they have no exit status at launch. Shortly after, Claudio checks whether each is still running and reports one that already exited, with the tail of its log:

```
post_start: "pnpm dev" exited with 127 within 750ms — the instance is up but this command is not running (log: /tmp/claudio-post-start.log)
    sh: pnpm: not found
```

This catches a command that can't start at all, not a server that dies a minute later. Either way the instance stays running: a dead `post_start` command is reported, never fatal.

For the full output:

```bash
claudio logs <id> --post-start
```

Holds stdout and stderr from every `post_start` command. Truncated at each provision, so it describes the container currently running.

A repo declaring an `image:` section with `apt`/`npm_global`/`dockerfile` needs its own image layer, built on top of the shared base — build it explicitly:

```bash
claudio image build --repo /path/to/your/clone
```

`create` never builds an image as a side effect. If the image it needs doesn't exist, it prints the exact `claudio image build --repo ...` command to run.
