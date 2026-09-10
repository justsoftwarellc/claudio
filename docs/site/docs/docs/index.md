# claudio

claudio runs several [Claude Code](https://claude.ai/code) sessions in parallel, each in its own sandboxed container with a real git checkout and reachable ports. Instead of one Claude Code session tying up your one working tree, each session gets its own worktree, its own container, and its own ports — so you can run five lines of work at once without them stepping on each other.

## Requirements

[`./install.sh`](#install) checks for all of these and installs what's missing,
so you don't need to work through this list by hand:

- A Docker-API-compatible container runtime. **[OrbStack](https://orbstack.dev/) is recommended** on macOS; Docker Desktop and native Linux Docker are also supported.
- A Claude subscription, and the `claude` CLI installed (`claude setup-token` — see [Getting Started](./guide/getting-started.md)).
- Go 1.25+ to build claudio itself (no prebuilt binary yet).

One thing it can't do for you: SSH access to the repos you want to work in
(claudio clones over `git@host:path`, `ssh://`, or `https://` — see [Cloning](./guide/getting-started.md#cloning)).

## Install

```bash
git clone https://github.com/justsoftwarellc/claudio.git
cd claudio
./install.sh
```

That checks for everything claudio needs (Homebrew, git, Go, a container
runtime, the `claude` CLI), installs whatever's missing, builds the binary,
puts it on your `PATH`, and builds the base image. It's safe to re-run — every
step skips what's already done, so it doubles as a repair tool when one piece
has drifted.

| Flag | |
|---|---|
| `./install.sh --check` | report what's missing, change nothing |
| `./install.sh --yes` | never prompt, install missing dependencies |
| `./install.sh --skip-image` | skip the (slow) base image build |

<details>
<summary>Installing by hand instead</summary>

The script automates exactly these steps. Do them yourself if you want the
binary somewhere else, or you'd rather install the dependencies your own way.

Build to `~/go/bin` (or your `GOPATH`/`GOBIN`), which needs no `sudo`:

```bash
go build -o "$(go env GOPATH)/bin/claudio" ./cmd/claudio
```

If `claudio` isn't found afterwards, that directory isn't on your `PATH` — add it:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"   # add to ~/.zshrc to make it stick
```

To install somewhere system-wide like `/usr/local/bin` instead, that path is root-owned, so the copy needs elevation:

```bash
go build -o ./claudio ./cmd/claudio && sudo mv ./claudio /usr/local/bin/claudio
```

Then build the base image every instance runs on:

```bash
claudio image build
```

</details>

The base image is `claudio/base:latest` (Node, git, tmux, Claude Code, matched
to your host UID/GID). Re-run `claudio image build` after pulling changes to
`image/`; Docker's layer cache makes a no-op rebuild nearly free.

## The 60-second path

```bash
export CLAUDE_CODE_OAUTH_TOKEN=$(claude setup-token)   # one-time; see Getting Started
claudio create git@github.com:acme/web.git
claudio ls
claudio attach brave-otter   # use the ID claudio printed
```

`create` clones the repo (once), adds a worktree, builds or reuses an image, and starts a container. `attach` drops you into the real Claude Code TUI inside it, with full color and resize support. Detach with `Ctrl-b d`; the session keeps running. `claudio ls --all` shows stopped instances too.

Two ways out, and they mean different things. `Ctrl-b d` **detaches**: the session keeps running, and you reattach later to find it where you left it. Quitting Claude Code itself (double `Ctrl-C`) **ends** the session and puts you back on your own shell — the instance is still there, and the next `claudio attach` starts a fresh session in it. If Claude Code ever fails to start, you get a shell inside the container instead of a closed session, so there's somewhere to debug from.

Continue to [Getting Started](./guide/getting-started.md) for auth setup, workspace layout, and ports, or jump straight to the [Configuration reference](./guide/configuration.md) for `.claudio.yml`/`config.yml`.
