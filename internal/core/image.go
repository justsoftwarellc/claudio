package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/coreerr"
	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/imagebuild"
)

// BuildImageParams is `claudio image build`'s input (ROD-96,
// docs/architecture.md §7.1). RepoPath, when set, additionally builds a
// second layer FROM the base for that repo's .claudio.yml image: section
// — RepoURL identifies the repo for tagging (see imagebuild.RepoImageTag)
// the same way CreateParams.RepoURL does elsewhere.
type BuildImageParams struct {
	RepoPath   string `json:"repo_path,omitempty"`
	RepoURL    string `json:"repo_url,omitempty"`
	UserUID    int    `json:"user_uid"`
	UserGID    int    `json:"user_gid"`
	DockerHost string `json:"docker_host,omitempty"`
}

// BuildImageResult reports what was actually built — RepoImage is empty
// when RepoPath's .claudio.yml has no image: section worth a second
// layer (NeedsRepoImage), which is the common case and not an error.
type BuildImageResult struct {
	BaseImage string `json:"base_image"`
	RepoImage string `json:"repo_image,omitempty"`
}

// BuildImage runs `claudio image build`'s full flow: the base image
// always, then a repo-specific layer on top of it if RepoPath names a
// repo whose .claudio.yml asks for one. progress mirrors
// CreateInstance's own caller-supplied sink (docs/architecture.md §12.4)
// since a Docker build is exactly the kind of long-running, network-
// dependent operation that must not sit silent — but carries raw build
// output lines rather than store.ProvisionStep, since a build has no
// provisioning state machine of its own; the type stays a plain
// func(string) rather than ProgressFunc for that reason.
func BuildImage(ctx context.Context, params BuildImageParams, progress func(string)) (BuildImageResult, error) {
	op := "core: image build"
	buildArgs := imagebuild.BuildArgsForHost(params.UserUID, params.UserGID)

	if err := imagebuild.BuildBase(ctx, params.DockerHost, buildArgs, progress); err != nil {
		return BuildImageResult{}, coreerr.Wrap(coreerr.ProvisionFailed, op, err)
	}
	result := BuildImageResult{BaseImage: imagebuild.BaseImage}

	if params.RepoPath == "" {
		return result, nil
	}

	repoCfg, err := config.LoadRepoConfig(filepath.Join(params.RepoPath, ".claudio.yml"))
	if err != nil {
		return BuildImageResult{}, coreerr.Wrap(coreerr.InvalidInput, op, err)
	}

	// An escape-hatch Dockerfile (.claudio/Dockerfile or
	// .devcontainer/Dockerfile) can apply even when .claudio.yml declares
	// no image: section at all — docs/architecture.md §7.1 treats their
	// mere presence on disk as sufficient, independent of the YAML.
	hasConventionalEscapeHatch := fileExists(filepath.Join(params.RepoPath, ".claudio", "Dockerfile")) ||
		fileExists(filepath.Join(params.RepoPath, ".devcontainer", "Dockerfile"))
	if !imagebuild.NeedsRepoImage(repoCfg.Image) && !hasConventionalEscapeHatch {
		return result, nil
	}

	repoTag, err := imagebuild.BuildRepoImage(ctx, params.DockerHost, params.RepoURL, params.RepoPath, imagebuild.BaseImage, repoCfg.Image, buildArgs, progress)
	if err != nil {
		return BuildImageResult{}, coreerr.Wrap(coreerr.ProvisionFailed, op, err)
	}
	result.RepoImage = repoTag
	return result, nil
}

// EnsureImageAvailable checks that image already exists locally, and if
// not, returns a *coreerr.Error naming `claudio image build` (and, when
// repoHint is non-empty, its --repo flag) instead of letting
// engine.CreateAndStart fail with Docker's own "no such image" text —
// docs/architecture.md's general stance that a failure a user can act on
// must say what to do, not just that something broke (ROD-96, mirroring
// ROD-112's OOMKilled treatment). Called from provisionContainer, ahead
// of CreateAndStart, so this is the message a caller actually sees.
func EnsureImageAvailable(ctx context.Context, dockerHost, image, repoHint string) error {
	exists, err := engine.ImageExists(ctx, dockerHost, image)
	if err != nil {
		return coreerr.Wrap(coreerr.Unavailable, "core: check image", err)
	}
	if exists {
		return nil
	}
	msg := fmt.Sprintf("image %q does not exist — run `claudio image build` to build it", image)
	if repoHint != "" {
		msg = fmt.Sprintf("image %q does not exist — run `claudio image build --repo %s` to build it", image, repoHint)
	}
	return coreerr.Wrap(coreerr.Unavailable, "core: provision", fmt.Errorf("%s", msg))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
