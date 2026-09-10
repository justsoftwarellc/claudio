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
