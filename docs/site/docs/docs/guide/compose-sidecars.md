# Adding a Database or Another Service

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

**Client tools, not the service itself, go in the agent image** — if your app needs `psql` or `redis-cli` to talk to a sidecar, declare that in [`.claudio.yml`'s `image:` section](./configuration.md), not as a service.
