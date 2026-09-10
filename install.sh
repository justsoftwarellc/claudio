#!/usr/bin/env bash
# claudio installer — checks for every dependency, installs what's missing,
# builds the binary, puts it on your PATH, and builds the base image.
#
# Safe to re-run: every step detects what's already there and skips it, so
# this doubles as a repair tool when one piece of the setup has drifted.
#
#   ./install.sh                 # the whole thing
#   ./install.sh --check         # report what's missing, change nothing
#   ./install.sh --skip-image    # skip the (slow) base image build
#   ./install.sh --yes           # never prompt; install missing deps
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

CHECK_ONLY=0
SKIP_IMAGE=0
ASSUME_YES=0

for arg in "$@"; do
	case "$arg" in
	--check | -n) CHECK_ONLY=1 ;;
	--skip-image) SKIP_IMAGE=1 ;;
	--yes | -y) ASSUME_YES=1 ;;
	--help | -h)
		# Print the header comment block above, minus the shebang, so the
		# usage text has exactly one source.
		sed -n '2,${/^#/!q;p;}' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "install.sh: unknown option $arg (try --help)" >&2
		exit 2
		;;
	esac
done

# ---------------------------------------------------------------- output

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	BOLD=$'\033[1m' DIM=$'\033[2m' RED=$'\033[31m' GREEN=$'\033[32m' YELLOW=$'\033[33m' RESET=$'\033[0m'
else
	BOLD='' DIM='' RED='' GREEN='' YELLOW='' RESET=''
fi

step() { printf '\n%s==>%s %s%s%s\n' "$BOLD" "$RESET" "$BOLD" "$1" "$RESET"; }
ok() { printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$1"; }
info() { printf '  %s·%s %s\n' "$DIM" "$RESET" "$1"; }
warn() { printf '  %s!%s %s\n' "$YELLOW" "$RESET" "$1"; }
fail() {
	printf '  %s✗%s %s\n' "$RED" "$RESET" "$1" >&2
	exit 1
}

# MISSING collects what --check should report and what a real run installs.
MISSING=()
note_missing() { MISSING+=("$1"); }

# Ask before doing something that touches the machine. --yes says yes,
# --check says no, and a non-interactive shell (piped into bash) says yes
# too — there's no one there to answer, and the user opted in by running it.
confirm() {
	local prompt="$1"
	[ "$CHECK_ONLY" -eq 1 ] && return 1
	[ "$ASSUME_YES" -eq 1 ] && return 0
	[ -t 0 ] || return 0
	local reply
	printf '  %s?%s %s [Y/n] ' "$YELLOW" "$RESET" "$prompt"
	read -r reply </dev/tty || return 1
	case "$reply" in "" | y | Y | yes | YES) return 0 ;; *) return 1 ;; esac
}

OS="$(uname -s)"

# ---------------------------------------------------------------- homebrew
#
# On macOS everything else here (go, orbstack, node) comes from Homebrew, so
# it's the one dependency worth bootstrapping ourselves.

ensure_homebrew() {
	[ "$OS" = "Darwin" ] || return 0
	if command -v brew >/dev/null 2>&1; then
		ok "Homebrew $(brew --version | head -1 | awk '{print $2}')"
		return 0
	fi
	note_missing "Homebrew"
	warn "Homebrew not installed (needed to install Go and OrbStack)"
	if ! confirm "Install Homebrew now?"; then
		info "Skipping. Install it yourself: https://brew.sh"
		return 1
	fi
	/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
	# The installer prints a shellenv line rather than applying it; a
	# fresh install isn't on PATH until we do that ourselves.
	for prefix in /opt/homebrew /usr/local; do
		if [ -x "$prefix/bin/brew" ]; then
			eval "$("$prefix/bin/brew" shellenv)"
			break
		fi
	done
	command -v brew >/dev/null 2>&1 || fail "Homebrew installed but 'brew' still isn't on PATH"
	ok "Homebrew installed"
}

# ---------------------------------------------------------------- go
#
# go.mod pins the toolchain floor; read it rather than hardcoding a version
# here that drifts the moment go.mod is bumped.

required_go_version() {
	awk '/^go /{print $2; exit}' "$REPO_DIR/go.mod"
}

# Compare dotted versions without sort -V, which BSD sort lacks.
version_ge() {
	local have="$1" want="$2" i h w
	local -a hp wp
	IFS=. read -r -a hp <<<"$have"
	IFS=. read -r -a wp <<<"$want"
	for i in 0 1 2; do
		h="${hp[$i]:-0}" w="${wp[$i]:-0}"
		h="${h%%[!0-9]*}" w="${w%%[!0-9]*}"
		((10#${h:-0} > 10#${w:-0})) && return 0
		((10#${h:-0} < 10#${w:-0})) && return 1
	done
	return 0
}

ensure_go() {
	local want
	want="$(required_go_version)"
	if command -v go >/dev/null 2>&1; then
		local have
		have="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
		if version_ge "$have" "$want"; then
			ok "Go $have (need $want+)"
			return 0
		fi
		note_missing "Go $want+ (found $have)"
		warn "Go $have is older than the $want this project needs"
	else
		note_missing "Go $want+"
		warn "Go not installed (need $want+)"
	fi

	if [ "$OS" = "Darwin" ] && command -v brew >/dev/null 2>&1; then
		if confirm "Install Go with Homebrew?"; then
			brew install go
			ok "Go $(go env GOVERSION | sed 's/^go//') installed"
			return 0
		fi
	fi
	info "Install Go $want+ yourself: https://go.dev/dl/"
	return 1
}

# ---------------------------------------------------------------- runtime
#
# `docker info` and not `docker --version`: the CLI version says nothing
# about which daemon it actually talks to, and claudio itself detects the
# runtime this way. A machine with both Docker Desktop and OrbStack can
# report one from the client and use the other for every real command.

ensure_runtime() {
	if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
		local runtime
		runtime="$(docker info --format '{{.OperatingSystem}}' 2>/dev/null || echo 'unknown')"
		ok "Container runtime reachable ($runtime)"
		return 0
	fi

	if [ "$OS" != "Darwin" ]; then
		note_missing "a running Docker daemon"
		warn "No reachable Docker daemon (\`docker info\` failed)"
		info "Install Docker Engine and start it: https://docs.docker.com/engine/install/"
		return 1
	fi

	if [ -d /Applications/OrbStack.app ]; then
		note_missing "OrbStack running"
		warn "OrbStack is installed but its daemon isn't reachable"
		if confirm "Start OrbStack now?"; then
			open -a OrbStack
			# The daemon takes a few seconds to accept connections after
			# the app launches; poll rather than guessing a sleep.
			local i
			for i in $(seq 1 30); do
				docker info >/dev/null 2>&1 && {
					ok "OrbStack running"
					return 0
				}
				sleep 1
			done
			warn "OrbStack didn't become reachable within 30s — start it manually"
		fi
		return 1
	fi

	note_missing "OrbStack (or another Docker runtime)"
	warn "No container runtime found"
	if command -v brew >/dev/null 2>&1 && confirm "Install OrbStack with Homebrew?"; then
		brew install --cask orbstack
		open -a OrbStack
		local i
		for i in $(seq 1 60); do
			docker info >/dev/null 2>&1 && {
				ok "OrbStack running"
				return 0
			}
			sleep 1
		done
		warn "OrbStack installed; finish its first-run setup, then re-run this script"
		return 1
	fi
	info "Install OrbStack yourself: https://orbstack.dev"
	return 1
}

# ---------------------------------------------------------------- claude cli
#
# Not needed to build claudio, but needed to get a token before the first
# `claudio create` — so a missing one is a warning, never fatal.

ensure_claude_cli() {
	if command -v claude >/dev/null 2>&1; then
		ok "claude CLI ($(command -v claude))"
		return 0
	fi
	note_missing "claude CLI"
	warn "claude CLI not found (needed for \`claude setup-token\`)"
	if command -v npm >/dev/null 2>&1; then
		if confirm "Install it with npm?"; then
			npm install -g @anthropic-ai/claude-code
			ok "claude CLI installed"
			return 0
		fi
	else
		info "npm not found — install Node first (brew install node)"
	fi
	info "Or install it yourself: https://claude.ai/code"
	return 1
}

# ---------------------------------------------------------------- editor
#
# `claudio open` needs an editor configured (ROD-130). It is asked for here
# because this is the one moment we already have the user's attention and a
# tty; every other path leaves them to discover the setting from an error.
#
# Never fatal, and never blocking: an unset editor only affects `claudio
# open`, so --yes, --check, and a piped-in shell all fall through to
# $EDITOR-or-nothing rather than stopping an install that is otherwise fine.

# Editors worth suggesting, GUI first — someone typing `claudio open` wants
# a window, and a terminal editor is the answer only if it's the one they
# actually configured.
EDITOR_CANDIDATES=(code cursor subl zed nvim vim)

ensure_editor() {
	# Already set is the common case on a re-run; installs are re-run often
	# enough that re-asking would be its own annoyance.
	local current
	if current="$("$BIN_DIR/claudio" config get editor 2>/dev/null)" && [ -n "$current" ]; then
		ok "editor: $current"
		return 0
	fi

	local suggestion=""
	for candidate in "${EDITOR_CANDIDATES[@]}"; do
		if command -v "$candidate" >/dev/null 2>&1; then
			suggestion="$candidate"
			break
		fi
	done
	# $EDITOR is a weaker signal than a detected GUI editor (it is usually
	# vi, meaning "edit this commit message", not "open this project"), so
	# it is the fallback rather than the first choice.
	if [ -z "$suggestion" ] && [ -n "${EDITOR:-}" ]; then
		suggestion="$(basename "${EDITOR%% *}")"
	fi

	if [ -z "$suggestion" ]; then
		warn "No editor detected for \`claudio open\`"
		info "Set one later: claudio config set editor <binary>"
		return 0
	fi

	# Non-interactive (--yes, --check, piped): take the suggestion silently
	# rather than prompting into a void or leaving it unset.
	if [ "$ASSUME_YES" -eq 1 ] || [ ! -t 0 ]; then
		if "$BIN_DIR/claudio" config set editor "$suggestion" >/dev/null 2>&1; then
			ok "editor: $suggestion"
		else
			info "Set one later: claudio config set editor <binary>"
		fi
		return 0
	fi

	local reply
	printf '  %s?%s Editor for \`claudio open\` [%s] ' "$YELLOW" "$RESET" "$suggestion"
	read -r reply </dev/tty || reply=""
	[ -z "$reply" ] && reply="$suggestion"

	if "$BIN_DIR/claudio" config set editor "$reply" >/dev/null 2>&1; then
		ok "editor: $reply"
	else
		# claudio itself rejects a binary that isn't on PATH, which is the
		# check we want; just don't let it fail the install.
		warn "Could not set editor to \"$reply\" (not found on PATH?)"
		info "Set one later: claudio config set editor <binary>"
	fi
}

# ---------------------------------------------------------------- git/ssh

ensure_git() {
	if command -v git >/dev/null 2>&1; then
		ok "git $(git --version | awk '{print $3}')"
	else
		note_missing "git"
		warn "git not installed"
		if [ "$OS" = "Darwin" ]; then
			info "Install the Xcode command line tools: xcode-select --install"
		else
			info "Install git with your package manager"
		fi
		return 1
	fi
}

# ---------------------------------------------------------------- install dir

# Where `go build -o` should put the binary: GOBIN if set, else GOPATH/bin,
# which is what `go install` would pick and needs no sudo.
install_dir() {
	local dir
	dir="$(go env GOBIN 2>/dev/null || true)"
	[ -n "$dir" ] || dir="$(go env GOPATH)/bin"
	printf '%s' "$dir"
}

# ---------------------------------------------------------------- PATH
#
# Add the install dir to PATH in the rc file of the user's login shell,
# guarded by a marker so re-running never appends a second copy.

shell_rc() {
	case "$(basename "${SHELL:-/bin/bash}")" in
	zsh) printf '%s' "${ZDOTDIR:-$HOME}/.zshrc" ;;
	bash)
		# macOS bash reads .bash_profile for login shells; Linux .bashrc.
		if [ "$OS" = "Darwin" ] && [ -f "$HOME/.bash_profile" ]; then
			printf '%s' "$HOME/.bash_profile"
		else
			printf '%s' "$HOME/.bashrc"
		fi
		;;
	fish) printf '%s' "$HOME/.config/fish/config.fish" ;;
	*) printf '%s' "$HOME/.profile" ;;
	esac
}

ensure_path() {
	local dir="$1" rc
	rc="$(shell_rc)"

	case ":$PATH:" in *":$dir:"*)
		ok "$dir is already on your PATH"
		return 0
		;;
	esac

	# Already written to the rc file by a previous run, just not loaded
	# into *this* shell — say so instead of appending a duplicate.
	if [ -f "$rc" ] && grep -q 'added by claudio install.sh' "$rc"; then
		ok "PATH entry already in $rc — run: source $rc"
		return 0
	fi

	if [ "$CHECK_ONLY" -eq 1 ]; then
		warn "$dir is not on your PATH (would add it to $rc)"
		return 1
	fi

	if ! confirm "Add $dir to your PATH in $rc?"; then
		info "Skipped. Add it yourself: export PATH=\"$dir:\$PATH\""
		return 1
	fi

	mkdir -p "$(dirname "$rc")"
	if [ "$(basename "$rc")" = "config.fish" ]; then
		printf '\n# added by claudio install.sh\nfish_add_path %s\n' "$dir" >>"$rc"
	else
		printf '\n# added by claudio install.sh\nexport PATH="%s:$PATH"\n' "$dir" >>"$rc"
	fi
	export PATH="$dir:$PATH"
	ok "Added to $rc (already applied to this run)"
}

# ---------------------------------------------------------------- main

printf '%sclaudio installer%s\n' "$BOLD" "$RESET"
[ "$CHECK_ONLY" -eq 1 ] && info "--check: reporting only, nothing will be installed"

step "Checking dependencies"
DEPS_OK=1
ensure_homebrew || DEPS_OK=0
ensure_git || DEPS_OK=0
ensure_go || DEPS_OK=0
ensure_runtime || DEPS_OK=0
ensure_claude_cli || true # not required to build

if [ "$CHECK_ONLY" -eq 1 ]; then
	step "Summary"
	if [ ${#MISSING[@]} -eq 0 ]; then
		ok "Everything claudio needs is present"
		exit 0
	fi
	for m in "${MISSING[@]}"; do warn "missing: $m"; done
	info "Re-run without --check to install these"
	exit 1
fi

# A missing Go toolchain is the one thing that makes the rest impossible.
command -v go >/dev/null 2>&1 || fail "Go is required to build claudio; install it and re-run"

step "Building claudio"
BIN_DIR="$(install_dir)"
mkdir -p "$BIN_DIR"
(cd "$REPO_DIR" && go build -o "$BIN_DIR/claudio" ./cmd/claudio)
ok "Built $BIN_DIR/claudio"

step "Checking PATH"
ensure_path "$BIN_DIR" || true

# Confirm the shell resolves the binary we just built, and not some older
# claudio earlier on PATH — a stale one on PATH is exactly the kind of
# thing that makes the next command fail confusingly.
if resolved="$(command -v claudio 2>/dev/null)"; then
	if [ "$resolved" != "$BIN_DIR/claudio" ]; then
		warn "\`claudio\` resolves to $resolved, not the build at $BIN_DIR/claudio"
	fi
fi

step "Editor"
ensure_editor

if [ "$SKIP_IMAGE" -eq 1 ]; then
	step "Base image"
	info "Skipped (--skip-image). Run \`claudio image build\` before your first instance."
elif docker info >/dev/null 2>&1; then
	step "Building the base image"
	info "This takes a few minutes the first time; later runs hit Docker's layer cache."
	"$BIN_DIR/claudio" image build
	ok "Base image ready"
else
	step "Base image"
	warn "Skipped: no reachable container runtime"
	info "Start it, then run: claudio image build"
fi

step "Done"
printf '\n'
if ! command -v claudio >/dev/null 2>&1 || [ "$(command -v claudio)" != "$BIN_DIR/claudio" ]; then
	info "Start a new shell (or: source $(shell_rc)) to pick up claudio"
fi
if ! command -v claude >/dev/null 2>&1; then
	info "Install the claude CLI, then get a token (below)"
fi
cat <<EOF
  Next:

    export CLAUDE_CODE_OAUTH_TOKEN=\$(claude setup-token)   # one-time
    claudio create git@github.com:acme/web.git
    claudio attach <id>

  See README.md for the rest.
EOF
