# Troubleshooting

**"every host port in N-M is taken"** — the configured port range is exhausted. Either widen `ports.range` in `~/.claudio/config.yml`, or free some up: `claudio ls` to see what's running, `claudio destroy` anything you don't need.

**"the container runtime is unreachable"** — Docker (or OrbStack) isn't running, or claudio can't reach it. Run `docker info` yourself first; if that works but claudio still can't connect, check `runtime.docker_host` in your config.

**Container exited immediately / "stopped (out of memory)"** — `claudio ls`/`claudio status` will say if it was an OOM kill specifically. Raise the limit with `--memory` on `create`, or in `.claudio.yml`'s `resources:`.

**Clone failed over SSH** — claudio clones exactly the way `git clone` would from your shell; if `git clone <repo>` doesn't work standalone, `claudio create` won't either. Check your SSH agent has the right key loaded.

**Sidecar unhealthy** — `claudio status <id>` lists each sidecar's live state; `claudio logs <id> --service <name>` gets its actual output.

**Files in the workspace are owned by the wrong user / need `sudo` to touch** — this shouldn't happen: the base image is built with `USER_UID`/`USER_GID` matched to your host user specifically to avoid it. If it does, `docker image inspect claudio/base:latest` and confirm the UID matches `id -u` on your host — a stale image built before a UID change is the most likely cause; `claudio image build` again.

**A change to the image isn't showing up in a running instance** — rebuilding the `claudio` binary doesn't help: the container's entrypoint ships in the *image*, not the CLI. `claudio restart` doesn't either — it recreates the container from whatever image is tagged now, but never rebuilds it. Use `claudio rebuild <id>`, which does both and reports the image it moved between (`Image 4e1269969011 -> 98e847299a9f`), or tells you the image was already current so you can go looking elsewhere.

**"no such instance"** — the ID or name doesn't match anything claudio knows about. `claudio ls --all` to see everything, including stopped instances (a destroyed instance is gone for good, not just hidden).
