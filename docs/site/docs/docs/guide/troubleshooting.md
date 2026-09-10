# Troubleshooting

**"every host port in N-M is taken"** — the configured port range is exhausted. Either widen `ports.range` in `~/.claudio/config.yml`, or free some up: `claudio ls` to see what's running, `claudio destroy` anything you don't need.

**Something in the setup is broken and you're not sure what** — `./install.sh --check` reports the state of every dependency without changing anything, and a plain `./install.sh` repairs what it finds (it skips whatever is already in place).

**"the container runtime is unreachable"** — Docker (or OrbStack) isn't running, or claudio can't reach it. Run `docker info` yourself first; if that works but claudio still can't connect, check `runtime.docker_host` in your config.

**Container exited immediately / "stopped (out of memory)"** — `claudio ls`/`claudio status` will say if it was an OOM kill specifically. Raise the limit with `--memory` on `create`, or in `.claudio.yml`'s `resources:`.

**Clone failed over SSH** — claudio clones exactly the way `git clone` would from your shell; if `git clone <repo>` doesn't work standalone, `claudio create` won't either. Check your SSH agent has the right key loaded.

**Sidecar unhealthy** — `claudio status <id>` lists each sidecar's live state; `claudio logs <id> --service <name>` gets its actual output.

**Files in the workspace are owned by the wrong user / need `sudo` to touch** — this shouldn't happen: the base image is built with `USER_UID`/`USER_GID` matched to your host user specifically to avoid it. If it does, `docker image inspect claudio/base:latest` and confirm the UID matches `id -u` on your host — a stale image built before a UID change is the most likely cause; `claudio image build` again.

**My dev server is gone after `claudio restart`** — expected: restart *replaces* the container rather than restarting a process inside it, so anything that was running is gone. `post_create` won't bring it back either, since that hook runs only at create. Put the command in `post_start`, which runs on every start — see [Configuration](./configuration.md#lifecycle-hooks-post_create-vs-post_start).

**`post_start` ran but nothing is listening** — `post_start` commands are launched in the background and never waited on, so claudio reports that the command was *spawned*, not that it worked; a server that dies a second later still looks fine. Read `docker exec claudio-<id> cat /tmp/claudio-post-start.log` for its actual stdout/stderr. The most common cause is binding `localhost` instead of `0.0.0.0` — a loopback-bound app is unreachable from the host even with its port mapped correctly.

**A change to the image isn't showing up in a running instance** — rebuilding the `claudio` binary doesn't help: the container's entrypoint ships in the *image*, not the CLI. `claudio restart` doesn't either — it recreates the container from whatever image is tagged now, but never rebuilds it. Use `claudio rebuild <id>`, which does both and reports the image it moved between (`Image 4e1269969011 -> 98e847299a9f`), or tells you the image was already current so you can go looking elsewhere.

**"Claude configuration file at /home/agent/.claude.json is corrupted"** — Claude Code found invalid JSON in its own config and won't start, so `claudio attach` drops you into a session that immediately errors out. Repair it with `claudio config restore <id>`, then `claudio restart <id>` to apply it (the config is only read at startup, so a session that's already up keeps running against the old one).

The restore rebuilds the file host-side, through the bind-mounted `home/` — it never needs the broken session to come up, and works on a stopped instance. It prefers Claude Code's own timestamped backups under `~/.claude/backups/`, so what comes back is the real config from minutes earlier rather than a bare template; failing that it falls back to the same defaults the image pre-seeds. The broken file is renamed aside to `.claude.json.broken-<timestamp>`, never deleted.

**"no such instance"** — the ID or name doesn't match anything claudio knows about. `claudio ls --all` to see everything, including stopped instances (a destroyed instance is gone for good, not just hidden).
