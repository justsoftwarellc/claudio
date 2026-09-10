# Configuration

## Resource limits

Every instance gets a memory/CPU/PID ceiling so a runaway build in one container can't take down the machine (or your other instances). Defaults are conservative — 6 GB memory, 4 CPUs, 512 PIDs — sized against your container runtime's actual memory budget (which is smaller than your host's total RAM if you're on OrbStack or Docker Desktop's own VM), not the host's full capacity. Override per-repo in `.claudio.yml`, or per-instance:

```bash
claudio create git@github.com:acme/web.git --memory 10g --cpus 6 --pids 1024
```

A local override always wins over what a repo's `.claudio.yml` asks for — `create` tells you when it's doing that, rather than silently picking a different number than what's committed. If an instance gets killed for exceeding its memory limit, `claudio ls`/`claudio status` say so explicitly (colored, and distinct from a plain stop) instead of leaving you to guess why it stopped.

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
```

### Lifecycle hooks: `post_create` vs `post_start`

The two hooks look similar and are not interchangeable. The difference is what a Claudio restart actually does: **it replaces the container**, it does not restart a process inside one. So work that persists on disk and work that has to be *running* have different schedules.

| | `post_create` | `post_start` |
|---|---|---|
| Runs on `create` | yes | yes |
| Runs on `start` / `restart` / `rebuild` | **no** | **yes** |
| Waits for the command to finish | yes | no — launched in the background |
| A failing command fails provisioning | yes | no (see below) |
| Use it for | `npm ci`, migrations, codegen | dev servers, workers, queue consumers |

The rule of thumb: if the command produces **files**, it belongs in `post_create`. If it produces a **process**, it belongs in `post_start`.

Putting `npm start` in `post_create` does not work — not because it is rejected, but because it would block `claudio create` forever waiting for a server that never exits, and it would not come back after a restart anyway.

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

With this, `claudio create` installs dependencies and starts the server, and `claudio restart <id>` brings the server back on its own — no manual step inside the container.

One requirement is easy to miss: **the server must bind `0.0.0.0`, not `localhost`.** Published ports forward to the container's external interface, so an app on loopback is unreachable from the host even when its port is mapped correctly. In practice that means `vite --host 0.0.0.0`, `next dev -H 0.0.0.0`, or `server.listen(port, '0.0.0.0')` — note that `HOST=0.0.0.0` is honoured by some tools and ignored by others (Vite among them), so prefer the explicit flag.

#### Why `post_start` can't report failures

`post_start` commands are launched detached and are not waited on, which is the only way a long-running server can start without blocking provisioning. The consequence is that a command has no meaningful exit status at the moment it is launched: claudio reports whether the command was *spawned*, not whether it went on to work. A server that dies a second later leaves a healthy-looking instance and no error.

The log is the place to look:

```bash
docker exec claudio-<id> cat /tmp/claudio-post-start.log
```

It holds stdout and stderr from every `post_start` command, and is truncated at each provision, so it always describes the container currently running rather than every restart in the instance's history.

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
