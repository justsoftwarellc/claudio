// Package session wraps the tmux operations Claudio needs against a
// running instance's container — docs/architecture.md's own package
// layout names this "tmux: create, send-keys, capture-pane" (§12.4).
//
// image/entrypoint.sh already runs `tmux new-session`/`send-keys` once,
// at container boot, to start the "claude" session itself — that stays
// inside the container's own shell script, not this package. What this
// package is for is the host side: driving that same tmux server from
// outside the container, the way `claudio attach` execs `tmux attach`
// directly (docs/architecture.md §9.1, ROD-100 — deliberately not
// through this package either, since replacing the process is what
// yields a real TTY, and any hop through `docker exec` here would cost
// that fidelity for nothing).
//
// SendKeys and CapturePane exist for the phase-2 daemon commands
// docs/architecture.md §9.3 describes (`claudio send`, `claudio logs`)
// — ROD-101, not yet built. There is no phase-1 caller for this package
// today; it exists now so ROD-101 has the primitive to build on rather
// than reinventing docker-exec-into-tmux plumbing at that point.
package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/rodrigomorales/claudio/internal/engine"
)

// SessionName is the tmux session every Claudio container's entrypoint
// creates (image/entrypoint.sh) and `claudio attach` targets
// (docs/architecture.md §9.1) — a single well-known name because each
// container hosts exactly one agent session, never several.
const SessionName = "claude"

// PaneCommand is what the session's single pane runs (ROD-116). It is
// defined here as well as in image/entrypoint.sh because `claudio
// attach` recreates a missing session (docs/architecture.md §9.1) and
// must recreate it running the same thing the entrypoint would have.
//
// The pane's process is the session's only process, so its exit status
// decides the session's fate — which is exactly the behavior wanted:
// quitting Claude Code deliberately (a double Ctrl-C, which exits 0)
// ends the pane, so tmux tears down the session and the user lands back
// on their host shell. A nonzero exit — a crash, a bad credential —
// falls through to an interactive shell instead, keeping the session
// alive to debug in; leaving that shell retries Claude Code rather than
// stranding the user.
const PaneCommand = "while true; do claude && break; bash -l; done"

// SendKeys injects keystrokes into the container's tmux session via
// `tmux send-keys`, followed by Enter — docs/architecture.md §9.3: "send
// writes to the tmux pane via tmux send-keys, which is how the session
// receives input regardless of whether a human is attached." This is
// how a non-interactive caller (the future `claudio send`, or a daemon
// acting on a webhook) drives the same session a human could otherwise
// only reach by attaching.
func SendKeys(ctx context.Context, host, containerID, keys string) error {
	// engine.RunInContainer's sh -c wrapping is exactly what's needed
	// here too: tmux's own CLI, not the Docker SDK, is the interface —
	// there is no tmux control-mode Go client in this codebase, and
	// shelling out to the same `tmux` binary the container already runs
	// is what image/entrypoint.sh itself does.
	_, exitCode, err := engine.RunInContainer(ctx, host, containerID, "",
		fmt.Sprintf("tmux send-keys -t %s %s C-m", SessionName, shellQuote(keys)))
	if err != nil {
		return fmt.Errorf("session: send-keys in %s: %w", containerID, err)
	}
	if exitCode != 0 {
		return fmt.Errorf("session: send-keys in %s: tmux exited %d", containerID, exitCode)
	}
	return nil
}

// CapturePane returns the tmux pane's current visible content via `tmux
// capture-pane -p` — the mechanism docs/architecture.md §9.4 explicitly
// rules out as the *primary* activity signal ("pattern-matching tmux
// capture-pane output would break whenever the TUI changes, whereas
// hooks are a supported interface") but keeps as a real, useful
// operation in its own right: `claudio logs` (§9.3) has nothing else to
// stream from a tmux-hosted session, hooks give attention state, not
// transcript content.
func CapturePane(ctx context.Context, host, containerID string) (string, error) {
	output, exitCode, err := engine.RunInContainer(ctx, host, containerID, "",
		fmt.Sprintf("tmux capture-pane -t %s -p", SessionName))
	if err != nil {
		return "", fmt.Errorf("session: capture-pane in %s: %w", containerID, err)
	}
	if exitCode != 0 {
		return "", fmt.Errorf("session: capture-pane in %s: tmux exited %d: %s", containerID, exitCode, output)
	}
	return output, nil
}

// shellQuote wraps s in single quotes for safe inclusion in the sh -c
// string RunInContainer builds, escaping any single quote in s itself
// per the standard shell idiom ('"'"') — SendKeys' keys argument is
// caller-supplied text (a prompt to inject), not a trusted literal, so
// it must not be interpolated into the shell command unescaped.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
