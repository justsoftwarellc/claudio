// Package imagebuild implements `claudio image build` (ROD-96,
// docs/architecture.md §7.1): building the base claudio/base:latest
// image, and generating+building a second, repo-specific layer on top of
// it when a repo's .claudio.yml asks for one.
//
// This package owns Dockerfile generation and tagging; internal/engine
// owns talking to the Docker daemon (BuildImage, ImageExists) — the same
// split as the rest of the codebase, where engine stays a pure
// Docker-facing layer that never imports internal/config (docs/architecture.md
// §12.4).
package imagebuild

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rodrigomorales/claudio/image"
	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/repo"
)

// BaseImage is the fixed tag `claudio create` defaults to and this
// package's BuildBase always produces — see docs/architecture.md §7.1.
const BaseImage = "claudio/base:latest"

const baseDockerfileName = "Dockerfile"
const escapeHatchDockerfileName = "Dockerfile.escapehatch" // distinct name: never collides with a generated "Dockerfile" copied into the same build context

// BuildArgsForHost returns the USER_UID/USER_GID build args matched to
// the real host user, per docs/architecture.md §7.1: "UID/GID matching is
// a build arg, not a baked constant." Uses os.Getuid/os.Getgid directly
// rather than shelling out to `id` — the whole point is the *actual*
// values this process is running as, and the Go stdlib already reports
// them without a subprocess.
func BuildArgsForHost(uid, gid int) map[string]string {
	return map[string]string{
		"USER_UID": fmt.Sprintf("%d", uid),
		"USER_GID": fmt.Sprintf("%d", gid),
	}
}

// BuildBase builds and tags claudio/base:latest from the Dockerfile
// embedded in the claudio binary (image/embed.go) — not from an image/
// directory on disk, so this works from a claudio binary run anywhere,
// per this issue's own reasoning: a phase-1 CLI cannot assume it is
// invoked from inside a checkout of this repo.
func BuildBase(ctx context.Context, host string, buildArgs map[string]string, progress func(string)) error {
	return buildBaseTagged(ctx, host, BaseImage, buildArgs, progress)
}

// buildBaseTagged is BuildBase's real implementation, parameterized on
// the tag — split out so tests can build the real base Dockerfile
// (verifying UID/GID matching and Docker's layer cache end to end)
// without racing or clobbering a developer's own real claudio/base:latest,
// which BuildBase's fixed tag would otherwise force.
func buildBaseTagged(ctx context.Context, host, tag string, buildArgs map[string]string, progress func(string)) error {
	return engine.BuildImage(ctx, host, engine.BuildSpec{
		Files: []engine.BuildFile{
			{Name: baseDockerfileName, Contents: image.Dockerfile},
			{Name: "entrypoint.sh", Contents: image.Entrypoint},
		},
		Dockerfile: baseDockerfileName,
		Tags:       []string{tag},
		BuildArgs:  buildArgs,
		Progress:   writerFunc(progress),
	})
}

// RepoImageTag derives the per-repo image tag from repoURL the same way
// internal/repo.Slug derives the on-disk root directory name, so
// different repos never collide on one tag and the same repo always
// rebuilds the same tag rather than accumulating new ones. Reusing
// repo.Slug (rather than a second ad hoc derivation) is deliberate —
// see this package's own doc and repo.Slug's: "deterministic so a second
// create against the same repo finds the same root rather than cloning
// again," the identical property this tag needs.
func RepoImageTag(repoURL string) (string, error) {
	slug, err := repo.Slug(repoURL)
	if err != nil {
		return "", err
	}
	// Docker repository names must be all-lowercase — verified
	// empirically: a repo.Slug containing any uppercase segment (a
	// mixed-case GitHub org/repo name, or a local path with uppercase
	// components) produced a tag Docker's own API rejected outright with
	// "invalid reference format: repository name ... must be lowercase."
	// repo.Slug itself doesn't lowercase (its own job is a stable,
	// human-legible directory name, and directory names are legitimately
	// case-sensitive on the host), so this is this function's own
	// responsibility, not a bug to fix upstream in repo.Slug.
	return "claudio/repo-" + strings.ToLower(slug) + ":latest", nil
}

// NeedsRepoImage reports whether cfg's image section asks for anything
// beyond the base image — an empty image: section (or one that only sets
// base, since base already determines the *base* image, not this repo
// layer) needs no second build at all.
func NeedsRepoImage(cfg config.Image) bool {
	return len(cfg.Apt) > 0 || len(cfg.NpmGlobal) > 0 || cfg.Dockerfile != ""
}

// BuildRepoImage builds the second, repo-specific layer described by
// docs/architecture.md §7.1 for the repo at repoPath, FROM baseImage
// (normally BaseImage, but overridable — e.g. a caller that just built a
// non-default base tag). Tags the result with RepoImageTag(repoURL).
//
// Escape hatch semantics (§7.1: "repos with .devcontainer/Dockerfile or
// .claudio/Dockerfile build FROM the resolved base as an escape hatch"):
// when cfg.Dockerfile names a file (or one of those two conventional
// paths exists), that file is used AS the entire repo-layer Dockerfile —
// it must itself start with a FROM line naming the resolved base image,
// and apt/npm_global are ignored, not layered on top of it. This matches
// the doc's own escape-hatch framing: a full custom Dockerfile the repo
// controls end to end, not a fragment Claudio appends to. Only when no
// escape hatch is found does this package generate the apt/npm_global
// Dockerfile itself.
func BuildRepoImage(ctx context.Context, host, repoURL, repoPath, baseImage string, cfg config.Image, buildArgs map[string]string, progress func(string)) (tag string, err error) {
	tag, err = RepoImageTag(repoURL)
	if err != nil {
		return "", err
	}

	escapeHatchPath, escapeHatchContents, err := resolveEscapeHatch(repoPath, cfg)
	if err != nil {
		return "", err
	}

	var files []engine.BuildFile
	var dockerfileName string
	if escapeHatchContents != nil {
		dockerfileName = escapeHatchDockerfileName
		files = []engine.BuildFile{{Name: dockerfileName, Contents: escapeHatchContents}}
	} else {
		dockerfileName = baseDockerfileName
		files = []engine.BuildFile{{Name: dockerfileName, Contents: generateRepoDockerfile(baseImage, cfg)}}
	}

	if err := engine.BuildImage(ctx, host, engine.BuildSpec{
		Files:      files,
		Dockerfile: dockerfileName,
		Tags:       []string{tag},
		BuildArgs:  buildArgs,
		Progress:   writerFunc(progress),
	}); err != nil {
		if escapeHatchPath != "" {
			return "", fmt.Errorf("imagebuild: build %s from %s: %w", tag, escapeHatchPath, err)
		}
		return "", fmt.Errorf("imagebuild: build %s: %w", tag, err)
	}
	return tag, nil
}

// resolveEscapeHatch returns the escape-hatch Dockerfile's contents, and
// the path it was read from for error messages — both empty/nil when no
// escape hatch applies. cfg.Dockerfile, when set, is authoritative over
// the two conventional paths §7.1 also documents (an explicit config
// value always beats an implicit convention); when unset, .claudio/Dockerfile
// and .devcontainer/Dockerfile are tried in that order, matching the
// order the doc lists them.
func resolveEscapeHatch(repoPath string, cfg config.Image) (path string, contents []byte, err error) {
	candidates := []string{}
	if cfg.Dockerfile != "" {
		candidates = append(candidates, cfg.Dockerfile)
	} else {
		candidates = append(candidates, filepath.Join(".claudio", "Dockerfile"), filepath.Join(".devcontainer", "Dockerfile"))
	}

	for _, rel := range candidates {
		full := filepath.Join(repoPath, rel)
		data, readErr := os.ReadFile(full)
		if readErr == nil {
			return rel, data, nil
		}
		if !os.IsNotExist(readErr) {
			return "", nil, fmt.Errorf("imagebuild: read escape-hatch Dockerfile %s: %w", full, readErr)
		}
		if cfg.Dockerfile != "" {
			// An explicit path that doesn't exist is a config error, not a
			// "fall through to auto-detect" situation — auto-detection only
			// applies when the repo said nothing at all.
			return "", nil, fmt.Errorf("imagebuild: image.dockerfile %q does not exist in %s", cfg.Dockerfile, repoPath)
		}
	}
	return "", nil, nil
}

// generateRepoDockerfile builds the second-layer Dockerfile
// docs/architecture.md §7.1 describes: FROM the resolved base, apt
// packages via apt-get install, npm_global packages via npm install -g.
// Root runs both installs (matching the base image's own apt-get RUN,
// which also runs before USER agent is set) since apt-get needs root
// regardless, and installing npm_global packages under
// NPM_CONFIG_PREFIX=/opt/claude-code (set by the base image) needs
// write access to a root-owned path — see image/Dockerfile's own comment
// on why that install directory is root-owned.
func generateRepoDockerfile(baseImage string, cfg config.Image) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s\n", baseImage)
	fmt.Fprintln(&b, "USER root")
	if len(cfg.Apt) > 0 {
		fmt.Fprintln(&b, "RUN apt-get update && apt-get install -y --no-install-recommends \\")
		for _, pkg := range cfg.Apt {
			// Every package line ends in a line continuation, including
			// the last one — it continues onto the "&& rm -rf" line below,
			// not the next Dockerfile instruction. Found the hard way: a
			// single-package list with no trailing "\" on its one line
			// made the following "&& rm -rf ..." line its own instruction,
			// which Docker rejects as "unknown instruction: &&".
			fmt.Fprintf(&b, "        %s \\\n", pkg)
		}
		fmt.Fprintln(&b, "    && rm -rf /var/lib/apt/lists/*")
	}
	if len(cfg.NpmGlobal) > 0 {
		fmt.Fprintf(&b, "RUN npm install -g %s\n", strings.Join(cfg.NpmGlobal, " "))
	}
	fmt.Fprintln(&b, "USER agent")
	return []byte(b.String())
}

// writerFunc adapts progress to an io.Writer, returning a genuinely nil
// io.Writer (not a typed-nil *lineWriter boxed into a non-nil interface)
// when progress is nil — engine.BuildSpec.Progress's own nil check
// (`if spec.Progress == nil`) only catches the former; found the hard
// way, a typed-nil *lineWriter reached jsonmessage.DisplayJSONMessagesStream
// and panicked on the first Write.
func writerFunc(progress func(string)) io.Writer {
	if progress == nil {
		return nil
	}
	return &lineWriter{fn: progress}
}

// lineWriter adapts a func(string) progress callback to an io.Writer,
// the shape engine.BuildSpec.Progress wants (Docker's own
// jsonmessage.DisplayJSONMessagesStream writes formatted lines to an
// io.Writer) — mirrors core.ProgressFunc's role for create/start without
// coupling this package to core's specific ProgressEvent type, since
// image build has no store.ProvisionStep of its own to report against.
type lineWriter struct{ fn func(string) }

func (w *lineWriter) Write(p []byte) (int, error) {
	w.fn(string(p))
	return len(p), nil
}
