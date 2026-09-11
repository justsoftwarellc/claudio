# Getting Started

## First-run auth

Claudio needs an Anthropic credential to give each container. Run:

```bash
claude setup-token
```

This is a one-time interactive step that converts your existing Claude subscription into a long-lived token, so instances bill against your subscription rather than metered API usage. Export the result as `CLAUDE_CODE_OAUTH_TOKEN` (or set `ANTHROPIC_API_KEY` instead, if you'd rather use metered billing) before running `claudio create`/`start`/`restart` — claudio reads it from your shell environment and injects it into the container it starts.

**Current limitation**: claudio does not yet store this credential anywhere (a host keychain, `~/.claudio/auth/`) — you need it exported in whatever shell you run `claudio create` from, every time. It's also the *same* credential in every container: an agent that reads its own environment holds your subscription token, and revoking it stops every running instance at once. A credential broker that fixes both of these is planned but not built yet.

## Where the files are

```bash
claudio cd brave-otter
```

prints the instance's workspace path — a plain directory on your host, openable in any editor, not something living only inside the container. Wrap it in a shell function for `cd`-in-place:

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

An instance's app doesn't get port 3000 — it gets whatever's free in `43000–43999` (configurable, see [Configuration](./configuration.md)) on `127.0.0.1`. This is what lets N instances of the same repo run at once without their dev servers colliding. See the mapping with:

```bash
claudio status brave-otter   # or: claudio ls, which shows it in a column
```

If a port claudio guessed is wrong (or a service it didn't detect needs one), correct it with `claudio ports <id> --add <container-port>` — see [Configuration](./configuration.md) for declaring one permanently instead.

## Working with several instances

This is the actual point of the tool. `claudio ls` lists everything running (add `--all` for stopped instances too); `claudio ls --json` if you're scripting against it. Each instance is independent: its own branch, its own container, its own ports, so you can have one running a long build, another mid-review, another exploring a fix, without any of them touching each other's files.

## Cloning

`claudio create <repo>` accepts `git@host:path`, `ssh://[user@]host/path`, or `https://host/path` — **not** a bare `owner/repo` shorthand. The repo is cloned once per remote URL and reused; each `create` against the same URL adds a new worktree rather than re-cloning.

### From a local directory

`claudio create .` clones from a directory on disk instead of a remote, for local-only or not-yet-pushed work:

```bash
cd ~/Projects/my-app
claudio create .
```

Anything path-shaped works — `.`, `..`, `./sub`, `~/Projects/my-app`, or an absolute path. If the directory isn't a git repo yet, claudio runs `git init` and commits what's there first, so unversioned work still gets an instance.

What comes along is what's **committed**:

| | Reaches the instance? |
|---|---|
| Committed history | ✅ |
| Local-only branches | ✅ (as `origin/*`, so `--branch` can check them out) |
| Uncommitted / staged work | ❌ |

Commit (or stash and commit) anything you want the agent to see. Your source repo is never modified — claudio doesn't commit on your behalf in a repo that already exists — and the instance's `origin` points back at your local directory, so the agent can push a finished branch straight back to it with no GitHub round-trip.

Because the root is keyed by full path, two same-named repos in different directories get separate clones.

For work with no upstream repo at all (a scratch analysis, a greenfield prototype), use:

```bash
claudio create --new my-experiment
```

which `git init`s a fresh root instead of cloning anything.

## Branches

By default, `create` makes a new branch named `claudio/<instance-id>` off the repo's default branch. Override it:

- `--branch <name>` checks out an *existing* branch — it must already exist in the repo.
- `--new-branch <name>` creates a new branch with the name you choose, instead of the generated default.

**Two instances cannot share a branch.** This isn't an arbitrary rule — it falls directly out of git worktrees, which refuse to check out the same branch twice. If you try, `create` reports the collision and offers a suggested alternative name (or `--yes` to accept it non-interactively) rather than failing with git's own raw error.

## Untracked containers

If claudio's state database is lost, rebuilt from a backup, or a container is created/removed outside claudio entirely, `claudio ls` shows it separately as **untracked** rather than silently ignoring it. Two ways to resolve one:

- `claudio adopt <container>` reconstructs a store row from the container's own labels, so claudio starts managing it again.
- `claudio forget <container>` removes it permanently, with no attempt to adopt it.
