package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rodrigomorales/claudio/internal/core"
	"github.com/rodrigomorales/claudio/internal/repo"
)

// cmdImage implements `claudio image build [--repo <path>]` (ROD-96,
// docs/architecture.md §7.1). Deliberately its own top-level verb rather
// than something `create` does automatically: a multi-minute,
// network-dependent Docker build must never happen as a surprise side
// effect of `create` (see this issue's own "decision already made").
func cmdImage(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: claudio image build [--repo <path>]")
		return 1
	}
	switch args[0] {
	case "build":
		return cmdImageBuild(ctx, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "claudio image: unknown subcommand %q\n", args[0])
		return 1
	}
}

func cmdImageBuild(ctx context.Context, args []string) int {
	var repoPath string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repo":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "claudio image build: --repo requires a value")
				return 1
			}
			repoPath = args[i]
		default:
			fmt.Fprintf(os.Stderr, "claudio image build: unknown flag %q\n", args[i])
			return 1
		}
	}

	var repoURL string
	if repoPath != "" {
		abs, err := filepath.Abs(repoPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "claudio image build:", err)
			return 1
		}
		repoPath = abs
		if info, err := os.Stat(repoPath); err != nil || !info.IsDir() {
			fmt.Fprintf(os.Stderr, "claudio image build: --repo %s is not a directory\n", repoPath)
			return 1
		}
		repoURL = repoIdentity(repoPath)
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio image build:", err)
		return 1
	}
	defer c.Close()

	result, err := c.BuildImage(ctx, core.BuildImageParams{
		RepoPath: repoPath,
		RepoURL:  repoURL,
		UserUID:  os.Getuid(),
		UserGID:  os.Getgid(),
	}, func(line string) {
		fmt.Fprint(os.Stderr, line)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio image build:", err)
		return 1
	}

	fmt.Printf("Built %s\n", result.BaseImage)
	if result.RepoImage != "" {
		fmt.Printf("Built %s\n", result.RepoImage)
	}
	return 0
}

// repoIdentity resolves the URL identity imagebuild.RepoImageTag derives
// the per-repo tag from (via internal/repo.Slug), for a --repo value that
// is a local filesystem path rather than the git remote URL Slug expects.
//
// Preferring the checkout's own "origin" remote (when it has one) means
// `claudio image build --repo <local-clone>` and a later `claudio create
// <same-remote-url>` land on the exact same tag without either command
// needing to know about the other — the whole point of deriving it from
// the repo's identity rather than the path. Falls back to a synthetic
// file:// URL over the absolute path — deterministic across repeated
// builds against the same local directory — whenever there's no remote
// AND whenever the remote it found isn't something repo.Slug can
// actually parse: verified empirically, `git remote add origin
// ../origin.git` (a relative path, which git accepts without complaint
// and which a local testing/clone setup can produce) makes `git remote
// get-url origin` return that same relative string, and repo.Slug's
// parser has no rule for a bare relative path — no "://" prefix, no ":"
// for the scp-like form. Trusting the remote unconditionally in that
// case would make image build fail outright with "cannot parse URL"
// instead of falling back to the path it already knows works.
func repoIdentity(repoPath string) string {
	fallback := "file://" + repoPath
	out, err := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin").Output()
	if err != nil {
		return fallback
	}
	url := strings.TrimSpace(string(out))
	if url == "" {
		return fallback
	}
	if _, err := repo.Slug(url); err != nil {
		return fallback
	}
	return url
}
