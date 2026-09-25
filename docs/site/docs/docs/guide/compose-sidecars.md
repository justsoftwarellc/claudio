# Adding a Database or Another Service

Don't install Postgres into the agent's own image. If your repo ships a `docker-compose.yml` (or `compose.yaml`), claudio detects it: `create` starts every service as its own sidecar container on a per-instance network, with the agent joined to it. The agent reaches `db` by name, as the repo already expects. Published ports are rewritten to ones claudio allocates, so two instances of the same repo never collide over host port 5432.

To declare a sidecar or two without a full compose file, put them in `.claudio.yml`:

```yaml
services:
  - name: db
    image: postgres:16
    env:
      POSTGRES_PASSWORD: dev
  - name: cache
    image: redis:7
```

These are internal-only, reached by service name and never published to the host. For a sidecar port reachable from your host, use a compose file.

`stop`/`start`/`restart`/`destroy` treat a compose-backed instance as one project. `stop` brings down every container and sidecar data survives; `destroy` removes everything, including sidecar volumes. `claudio status` shows each sidecar's state, and `claudio logs <id> --service db` reaches one sidecar's logs (omit `--service` to interleave all of them).

**Client tools go in the agent image, not the service.** An app needing `psql` or `redis-cli` declares it in [`.claudio.yml`'s `image:` section](./configuration.md), not as a service.

## Already running it yourself?

The above covers services the *repo* owns, started and stopped by claudio. For something **already running on your machine** — a MongoDB container you keep up all day — declare it as a [host service](./host-services.md) instead, and the container reaches it by name:

```bash
claudio create . --host-service mongo:27017
```

That service must publish its port to the host — see [Host Services](./host-services.md) for how to check.
