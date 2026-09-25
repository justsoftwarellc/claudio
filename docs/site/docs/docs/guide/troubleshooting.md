# Troubleshooting

**"every host port in N-M is taken"** — the configured port range is exhausted. Widen `ports.range` in `~/.claudio/config.yml`, or free some: `claudio ls` shows what's running, `claudio destroy` removes what you don't need.

**Something in the setup is broken** — `./install.sh --check` reports every dependency's state without changing anything; a plain `./install.sh` repairs what it finds.

**"the container runtime is unreachable"** — Docker (or OrbStack) isn't running, or claudio can't reach it. Run `docker info` yourself first; if that works but claudio still can't connect, check `runtime.docker_host` in your config.

**Container exited immediately / "stopped (out of memory)"** — `claudio ls` and `claudio status` report an OOM kill specifically. Raise the limit with `--memory` on `create`, or `resources:` in `.claudio.yml`.

**Clone failed over SSH** — claudio clones the way `git clone` does from your shell. If `git clone <repo>` fails on its own, `claudio create` will too. Check your SSH agent has the right key loaded.

**Sidecar unhealthy** — `claudio status <id>` lists each sidecar's live state; `claudio logs <id> --service <name>` gets its actual output.

**Workspace files are owned by the wrong user** — the base image is built with `USER_UID`/`USER_GID` matched to your host user. Run `docker image inspect claudio/base:latest` and confirm the UID matches `id -u`. A stale image built before a UID change is the usual cause: run `claudio image build` again.

**My dev server is gone after `claudio restart`** — restart *replaces* the container rather than restarting a process inside it, so anything running is gone. `post_create` won't bring it back; it runs only at create. Put the command in `post_start`, which runs on every start — see [Configuration](./configuration.md#lifecycle-hooks-post_create-vs-post_start).

**`post_start` ran but nothing is listening** — these commands are launched in the background and never waited on. Claudio checks shortly after launch and reports one that already exited, but a server dying later still looks fine at create time. Read `claudio logs <id> --post-start` for the full output. The usual cause is binding `localhost` instead of `0.0.0.0`: a loopback-bound app is unreachable from the host even with its port mapped.

**A host service name resolves but the connection is refused** — the name is declared, but nothing on your host is listening at that port. Usually the service runs in another container started without publishing one: `docker run -d mongo:7` keeps 27017 container-internal, where `-p 27017:27017` puts it on the host. Check that `docker ps` shows a `->` mapping in its `PORTS` column.

**A host service name doesn't resolve at all** — it isn't declared for that instance. Only declared names resolve. Add it with `--host-service <name>:<port>` at create, or `host_services:` in `.claudio.yml`, then `claudio restart <id>` to apply it. `claudio ports <id>` lists what the instance reaches under `DIRECTION: host`.

**A change to the image isn't showing up in a running instance** — rebuilding the `claudio` binary won't help: the entrypoint ships in the *image*, not the CLI. `claudio restart` won't either; it recreates the container from whatever image is tagged now without rebuilding it. Use `claudio rebuild <id>`, which does both and reports the image it moved between (`Image 4e1269969011 -> 98e847299a9f`), or says the image was already current.

**"Claude configuration file at /home/agent/.claude.json is corrupted"** — Claude Code found invalid JSON in its own config and won't start. Repair it with `claudio config restore <id>`, then `claudio restart <id>`; the config is read only at startup, so a running session keeps using the old one.

The restore works host-side through the bind-mounted `home/`, so it works on a stopped instance. It prefers Claude Code's timestamped backups under `~/.claude/backups/`, falling back to the defaults the image pre-seeds. The broken file is renamed to `.claude.json.broken-<timestamp>`, never deleted.

**"no such instance"** — the ID or name matches nothing claudio knows. `claudio ls --all` lists everything, including stopped instances. A destroyed instance is gone, not hidden.
