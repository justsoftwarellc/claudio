# Getting Started

## First-run auth

Claudio needs an Anthropic credential to give each container. Run:

```bash
claude setup-token
```

A one-time step converting your Claude subscription into a long-lived token, so instances bill against your subscription rather than metered API usage. Export it as `CLAUDE_CODE_OAUTH_TOKEN` (or `ANTHROPIC_API_KEY` for metered billing) before running `claudio create`/`start`/`restart`; claudio reads it from your environment and injects it into the container.

**Limitation**: claudio does not store this credential, so it must be exported in whatever shell you run `claudio create` from. It is also the same credential in every container — an agent that reads its own environment holds your subscription token, and revoking it stops every running instance at once.

## Where the files are

```bash
claudio cd brave-otter
```

prints the instance's workspace path — a plain directory on your host, openable in any editor. Wrap it in a shell function to `cd` in place:

```bash
cdc() { cd "$(claudio cd "$1")"; }
```

Or open it in your editor instead:

```bash
claudio open brave-otter
```

That needs an editor set once — `install.sh` offers to do it, or:

```bash
claudio config set editor code   # or cursor, subl, zed, nvim, …
```

See [Configuration](./configuration.md#editor) for the details.

Workspaces live under `~/.claudio/repos/<repo-slug>/worktrees/<instance-id>/`. The container mounts the whole repo root, not just the worktree — that's what makes the worktree's `.git` file resolve correctly inside the container.

## Ports

An instance's app gets whatever's free in `43000–43999` on `127.0.0.1` (configurable, see [Configuration](./configuration.md)), so several instances of one repo can run without their dev servers colliding. See the mapping with:

```bash
claudio status brave-otter   # or: claudio ls, which shows it in a column
```

If a detected port is wrong, or a service went undetected, add one with `claudio ports <id> --add <container-port>`. To declare it permanently, see [Configuration](./configuration.md).

## Working with several instances

`claudio ls` lists everything running — `--all` includes stopped instances, `--json` for scripting. Each instance has its own branch, container, and ports, so one can run a long build while another is mid-review, with no shared files between them.

## Cloning

`claudio create <repo>` accepts `git@host:path`, `ssh://[user@]host/path`, or `https://host/path` — **not** a bare `owner/repo` shorthand. The repo is cloned once per remote URL and reused; each `create` against the same URL adds a new worktree rather than re-cloning.

### From a local directory

`claudio create .` clones from a directory on disk instead of a remote, for local-only or not-yet-pushed work:

```bash
cd ~/Projects/my-app
claudio create .
```

Any path works — `.`, `..`, `./sub`, `~/Projects/my-app`, or absolute. If the directory isn't a git repo, claudio runs `git init` and commits what's there first.

What comes along is what's **committed**:

| | Reaches the instance? |
|---|---|
| Committed history | ✅ |
| Local-only branches | ✅ (as `origin/*`, so `--branch` can check them out) |
| Uncommitted / staged work | ❌ |

Commit anything you want the agent to see. Your source repo is never modified, and the instance's `origin` points back at your local directory, so the agent can push a finished branch straight back with no GitHub round-trip.

Clone roots are keyed by full path, so two same-named repos in different directories stay separate.

For work with no upstream repo at all (a scratch analysis, a greenfield prototype), use:

```bash
claudio create --new my-experiment
```

which `git init`s a fresh root instead of cloning anything.

## Branches

By default, `create` makes a new branch named `claudio/<instance-id>` off the repo's default branch. Override it:

- `--branch <name>` checks out an *existing* branch — it must already exist in the repo.
- `--new-branch <name>` creates a new branch with the name you choose, instead of the generated default.

**Two instances cannot share a branch** — git worktrees refuse to check out the same branch twice. `create` reports the collision and suggests a free name (`--yes` accepts it non-interactively).

## Untracked containers

If claudio's state database is lost or a container is created outside claudio, `claudio ls` lists it as **untracked**. Two ways to resolve one:

- `claudio adopt <container>` reconstructs a store row from the container's labels and resumes managing it.
- `claudio forget <container>` removes it permanently.
