# claudio

claudio runs several [Claude Code](https://claude.ai/code) sessions in parallel, each in its own sandboxed container with a real git checkout and reachable ports. Every session gets its own worktree, container, and ports, so several lines of work run at once without colliding.

## Requirements

[`./install.sh`](#install) checks for these and installs what's missing:

- A Docker-API-compatible container runtime. **[OrbStack](https://orbstack.dev/) is recommended** on macOS; Docker Desktop and native Linux Docker are also supported.
- A Claude subscription, and the `claude` CLI installed (`claude setup-token` — see [Getting Started](./guide/getting-started.md)).
- Go 1.25+ to build claudio itself (no prebuilt binary yet).

It can't set up SSH access to your repos. claudio clones over `git@host:path`, `ssh://`, or `https://` — see [Cloning](./guide/getting-started.md#cloning).

## Install

```bash
git clone https://github.com/justsoftwarellc/claudio.git
cd claudio
./install.sh
```

That installs what's missing (Homebrew, git, Go, a container runtime, the `claude` CLI), builds the binary, puts it on your `PATH`, and builds the base image. Safe to re-run: every step skips what's already done, so it also repairs a broken install.

| Flag | |
|---|---|
| `./install.sh --check` | report what's missing, change nothing |
| `./install.sh --yes` | never prompt, install missing dependencies |
| `./install.sh --skip-image` | skip the (slow) base image build |

<details>
<summary>Installing by hand instead</summary>

The script automates these steps. Do them by hand to put the binary elsewhere, or to install dependencies your own way.

Build to `~/go/bin` (or your `GOPATH`/`GOBIN`), which needs no `sudo`:

```bash
go build -o "$(go env GOPATH)/bin/claudio" ./cmd/claudio
```

If `claudio` isn't found afterwards, that directory isn't on your `PATH` — add it:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"   # add to ~/.zshrc to make it stick
```

For a system-wide path like `/usr/local/bin`, which is root-owned, the copy needs elevation:

```bash
go build -o ./claudio ./cmd/claudio && sudo mv ./claudio /usr/local/bin/claudio
```

Then build the base image every instance runs on:

```bash
claudio image build
```

</details>

The base image is `claudio/base:latest` — Node, git, tmux, Claude Code, matched to your host UID/GID. Re-run `claudio image build` after pulling changes to `image/`; a no-op rebuild is nearly free.

## The 60-second path

```bash
export CLAUDE_CODE_OAUTH_TOKEN=$(claude setup-token)   # one-time; see Getting Started
claudio create git@github.com:acme/web.git
claudio ls
claudio attach brave-otter   # use the ID claudio printed
```

`create` clones the repo once, adds a worktree, builds or reuses an image, and starts a container. `attach` drops you into the Claude Code TUI inside it.

Two ways out, meaning different things:

- `Ctrl-b d` **detaches**. The session keeps running; reattach to find it where you left it.
- Quitting Claude Code (double `Ctrl-C`) **ends** the session. The instance stays, and the next `claudio attach` starts a fresh session in it.

If Claude Code fails to start, you get a shell inside the container instead of a closed session.

Next: [Getting Started](./guide/getting-started.md) for auth, workspace layout, and ports, or the [Configuration reference](./guide/configuration.md) for `.claudio.yml` and `config.yml`.
