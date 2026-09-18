# Reaching a Service Already on Your Machine

[Sidecars](./compose-sidecars.md) are for services the *repo* owns — claudio starts them, and they live and die with the instance. This page is the other case: the thing you want is **already running on your machine** and you don't want a second copy of it. A MongoDB container you keep up all day, a Postgres from a `docker compose` stack you started yourself, an API server on another port.

Declare it, and the container reaches it by name:

```bash
claudio create . --host-service mongo:27017
```

Inside the container, `mongo:27017` is now that service — so `mongodb://mongo:27017/myapp` works from your app without changing a line of code. Repeat the flag for more than one service.

To make it permanent for everyone working on the repo, put it in `.claudio.yml` instead:

```yaml
host_services:
  - name: mongo         # becomes a hostname inside the container
    host: 27017         # the port it already listens on, on your host
```

A `--host-service` flag wins over the repo's declaration of the same name, so you can point `mongo` somewhere else for one instance without editing the committed file.

## The one requirement: the container must publish a port

This is the part that trips people up, and it is worth being precise about.

Claudio adds a **hostname**, not a tunnel. Each declared name becomes an `/etc/hosts` entry pointing at the *host gateway* — the address of your machine as seen from inside the container. So the service has to be reachable **on your host**, at that port.

For a service running in another container, that means it was started with a published port:

```bash
# ✅ reachable — port 27017 is published to the host
docker run -d --name mongo -p 27017:27017 mongo:7

# ❌ NOT reachable — no published port, so nothing is on the host's 27017
docker run -d --name mongo mongo:7
```

Check with `docker ps`: the `PORTS` column has to show a `->` mapping.

```
PORTS
0.0.0.0:27017->27017/tcp     ✅ reachable as a host service
27017/tcp                    ❌ container-internal only
```

If it isn't published, you have three options: restart it with `-p 27017:27017`, add a `ports:` entry to whatever compose file starts it, or — if the service really belongs to this project — let claudio own it as a [sidecar](./compose-sidecars.md) instead.

The same rule covers non-container services. A Postgres installed with Homebrew and listening on `localhost:5432` is already "on the host", so `--host-service db:5432` reaches it with nothing further to do.

## The port is the same on both sides

`--host-service mongo:27017` means "the thing on host port 27017, called `mongo` inside". There is no remapping.

A hosts entry maps a name to an *address*; there is no port component anywhere in it, so "reach host 27017 as `mongo:6000`" is not something this mechanism can express. Claudio rejects `mongo:27017:6000` with an explanation rather than accepting it and resolving the name to a port where nothing is listening.

If your code hardcodes a port that differs from the one the service actually uses, change the connection string — or publish the service on the port your code expects.

## Only what you declare resolves

An undeclared name does not work from inside the container. This is deliberate: pointing an instance at your own database is a reasonable thing to want, an agent discovering host services by guessing names is not.

It's also why this is worth thinking about once before turning it on. A host service is a door out of the sandbox, opened one name at a time — the service you name is genuinely reachable from inside the container, with whatever access it grants to anyone who can reach it.

One name is always mapped: `host.docker.internal`, which resolves to your host on every runtime. That grants no access by itself — it's a name for an address the container's network could already route to — but it means a declared service is the *only* new reachability you're adding.

## Seeing what's wired up

`claudio ports <id>` shows both directions, so you can tell what the container **serves** from what it merely **reaches**:

```
SERVICE  DIRECTION  CONTAINER  ADDRESS                 SOURCE
web      published  3000       http://127.0.0.1:43001  detected (next.config.js)
mongo    host       27017      mongo:27017             declared (.claudio.yml)
```

A `published` row is a port of yours the host can dial. A `host` row is a name your container can dial, rendered as the in-container address rather than a host URL, because that is the form you'd actually put in a connection string.

## Checking it from inside

If a connection isn't working, test the two halves separately:

```bash
# 1. Does the name resolve? (If not, it isn't declared.)
docker exec claudio-<id> getent hosts mongo

# 2. Is anything actually listening there? (If not, it isn't published.)
docker exec claudio-<id> node -e 'require("net").connect(27017,"mongo")
  .on("connect",()=>{console.log("open");process.exit(0)})
  .on("error",e=>{console.log("closed:",e.code);process.exit(1)})'
```

(The agent image ships Node and `curl`, but not `nc` — hence the one-liner.)

A name that resolves but refuses the connection is the signature of an unpublished container port — step back to [the publishing rule](#the-one-requirement-the-container-must-publish-a-port).

## A full example

A repo whose app talks to a MongoDB you run yourself:

```bash
docker run -d --name mongo -p 27017:27017 mongo:7
```

```yaml
# .claudio.yml
host_services:
  - name: mongo
    host: 27017

post_create:
  - npm ci

post_start:
  - npm start
```

```bash
MONGO_URL=mongodb://mongo:27017/myapp
```

Every instance created from this repo reaches the same MongoDB, and the connection string is identical to the one you use on the host — which is the point. Note that all instances share that one database; if you want each instance to get its own, that's a [sidecar](./compose-sidecars.md), not a host service.
