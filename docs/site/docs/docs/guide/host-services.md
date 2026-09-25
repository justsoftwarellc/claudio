# Reaching a Service Already on Your Machine

[Sidecars](./compose-sidecars.md) are for services the *repo* owns: claudio starts them, and they live and die with the instance. This page covers the other case — a service **already running on your machine** that you don't want a second copy of, like a MongoDB container you keep up all day.

Declare it, and the container reaches it by name:

```bash
claudio create . --host-service mongo:27017
```

Inside the container, `mongo:27017` is that service, so `mongodb://mongo:27017/myapp` works unchanged. Repeat the flag for more than one service.

To make it permanent for this repo, put it in `.claudio.yml` instead:

```yaml
host_services:
  - name: mongo         # becomes a hostname inside the container
    host: 27017         # the port it already listens on, on your host
```

A `--host-service` flag wins over a `.claudio.yml` declaration of the same name, so one instance can point `mongo` elsewhere without editing the file.

## The one requirement: the container must publish a port

Claudio adds a **hostname**, not a tunnel. Each declared name becomes an `/etc/hosts` entry pointing at the host gateway — your machine's address as seen from inside the container. The service must therefore be reachable **on your host**, at that port.

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

If it isn't published: restart it with `-p 27017:27017`, add a `ports:` entry to the compose file that starts it, or let claudio own it as a [sidecar](./compose-sidecars.md).

Non-container services follow the same rule. A Homebrew Postgres listening on `localhost:5432` is already on the host, so `--host-service db:5432` reaches it directly.

## The port is the same on both sides

`--host-service mongo:27017` means "the thing on host port 27017, called `mongo` inside". There is no remapping.

A hosts entry maps a name to an *address* and has no port component, so "reach host 27017 as `mongo:6000`" can't be expressed. Claudio rejects `mongo:27017:6000` rather than resolving the name to a port where nothing is listening.

If your code hardcodes a different port, change the connection string or publish the service on the port it expects.

## Only what you declare resolves

An undeclared name does not resolve inside the container. Pointing an instance at your own database is reasonable; an agent discovering host services by guessing names is not.

So weigh each one: a host service is a door out of the sandbox, opened a name at a time. The service you name is reachable from inside the container, with whatever access it grants to anyone who reaches it.

One name is always mapped: `host.docker.internal`, which resolves to your host on every runtime. It grants no access by itself — the container's network could already route to that address — so a declared service is the only new reachability you add.

## Seeing what's wired up

`claudio ports <id>` shows both directions, so you can tell what the container **serves** from what it merely **reaches**:

```
SERVICE  DIRECTION  CONTAINER  ADDRESS                 SOURCE
web      published  3000       http://127.0.0.1:43001  detected (next.config.js)
mongo    host       27017      mongo:27017             declared (.claudio.yml)
```

A `published` row is a port the host can dial. A `host` row is a name the container can dial, shown as the in-container address — the form you'd put in a connection string.

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

(The agent image ships Node and `curl`, but not `nc`.)

A name that resolves but refuses the connection means an unpublished container port — see [the publishing rule](#the-one-requirement-the-container-must-publish-a-port).

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

Every instance created from this repo reaches the same MongoDB, with the same connection string you use on the host. All instances share that one database — for a database per instance, use a [sidecar](./compose-sidecars.md).
